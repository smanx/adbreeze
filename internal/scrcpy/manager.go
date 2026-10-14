// Package scrcpy 实现投屏链路：推送并启动设备端 scrcpy-server，
// 建立视频流隧道，解析 scrcpy client 协议并扇出给多个 WebSocket 订阅者。
//
// 协议依据 scrcpy v2.4（与内嵌 server jar 版本严格配套）：
//   - server 启动: app_process + scid/tunnel_forward 等参数
//   - 握手: 1 字节 dummy + 64 字节设备名 + 12 字节视频元数据
//   - 帧: 12 字节帧头（flags+PTS / size）+ AnnexB 裸流
package scrcpy

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"adbreeze/internal/adb"
)

// Options 是投屏参数。
type Options struct {
	MaxSize    int    // 视频尺寸上限（较长边），0 表示不限
	MaxFPS     int    // 帧率上限，0 表示不限
	BitRate    int    // 视频码率 bps
	VideoCodec string // 目前仅支持 h264
	Audio      *bool  // 音频转发（Opus，设备播放声音采集），默认开启。
	// 采集激活时设备扬声器会被 remote submix 静音；关闭则设备扬声器正常出声
	// （参考 ya-webadb adb-scrcpy 各版本 Options 的 audio 开关）。
}

func (o Options) withDefaults() Options {
	if o.MaxSize == 0 {
		o.MaxSize = 1600
	}
	if o.BitRate == 0 {
		o.BitRate = 8_000_000
	}
	if o.MaxFPS == 0 {
		o.MaxFPS = 60
	}
	if o.VideoCodec == "" {
		o.VideoCodec = "h264"
	}
	if o.Audio == nil {
		t := true
		o.Audio = &t
	}
	return o
}

// Manager 管理所有设备的投屏流。流的生命周期与应用一致，
// 不绑定任何单个 HTTP 请求。
type Manager struct {
	adb           *adb.Manager
	jar           []byte
	serverVersion string
	appCtx        context.Context

	mu      sync.Mutex
	streams map[string]*Stream
}

func NewManager(ctx context.Context, m *adb.Manager, jar []byte, serverVersion string) *Manager {
	return &Manager{
		adb:           m,
		jar:           jar,
		serverVersion: serverVersion,
		appCtx:        ctx,
		streams:       make(map[string]*Stream),
	}
}

// Start 启动（或返回已存在的）设备投屏流。
func (m *Manager) Start(serial string, opts Options) (StreamInfo, error) {
	opts = opts.withDefaults()

	// 设备在线预检，快速给出明确错误
	online := false
	for _, d := range m.adb.Devices() {
		if d.Serial == serial && d.State == "device" {
			online = true
			break
		}
	}
	if !online {
		return StreamInfo{}, fmt.Errorf("设备 %s 不在线或未授权", serial)
	}

	m.mu.Lock()
	if s, ok := m.streams[serial]; ok {
		info := s.Info()
		m.mu.Unlock()
		return info, nil
	}
	m.mu.Unlock()

	// 在锁外启动（耗时数百毫秒），完成后再登记
	s, err := start(m.adb.Client(), serial, m.jar, m.serverVersion, opts)
	if err != nil {
		log.Printf("[scrcpy:%s] 启动失败: %v", serial, err)
		return StreamInfo{}, err
	}
	m.mu.Lock()
	// 并发窗口内可能已有人启动了另一条流：保留先到者，关闭后者
	if existing, ok := m.streams[serial]; ok {
		m.mu.Unlock()
		s.Stop()
		return existing.Info(), nil
	}
	m.streams[serial] = s
	m.mu.Unlock()

	// 应用退出时清理
	go func() {
		<-m.appCtx.Done()
		m.Stop(serial)
	}()
	log.Printf("[scrcpy:%s] 投屏已启动 (%dx%d @%s)", serial, s.info.Width, s.info.Height, s.info.DeviceName)
	return s.Info(), nil
}

// Stop 停止指定设备的投屏流。
func (m *Manager) Stop(serial string) error {
	m.mu.Lock()
	s, ok := m.streams[serial]
	if ok {
		delete(m.streams, serial)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	s.Stop()
	return nil
}

// Subscribe 订阅设备视频流。若流不存在返回错误。
func (m *Manager) Subscribe(serial string) (*Subscriber, func(), []byte, StreamInfo, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return nil, nil, nil, StreamInfo{}, fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	sub, cancel, cfg := s.Subscribe()
	return sub, cancel, cfg, s.Info(), nil
}

// VideoSize 返回设备当前视频尺寸（触摸注入的 screenW/H 必须与之匹配）。
func (m *Manager) VideoSize(serial string) (int, int, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return 0, 0, fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	w, h := s.VideoSize()
	return w, h, nil
}

// SendControl 向设备投屏流写入 ControlMessage。
func (m *Manager) SendControl(serial string, msg []byte) error {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	return s.SendControl(msg)
}

// RequestClipboard 请求设备上报剪贴板内容（经设备消息异步返回）。
func (m *Manager) RequestClipboard(serial string) error {
	return m.SendControl(serial, EncodeGetClipboard(0))
}

// Clipboard 返回最近一次设备剪贴板内容。
func (m *Manager) Clipboard(serial string) (string, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	return s.Clipboard(), nil
}

// WaitClipboard 请求剪贴板并等待新内容（超时返回当前值）。
func (m *Manager) WaitClipboard(serial string, timeout time.Duration) (string, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	if err := s.SendControl(EncodeGetClipboard(0)); err != nil {
		return "", err
	}
	return s.WaitClipboard(timeout), nil
}

// SubscribeAudio 订阅设备音频流（Opus）。若音频不可用返回错误。
func (m *Manager) SubscribeAudio(serial string) (*Subscriber, func(), []byte, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return nil, nil, nil, fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	return s.SubscribeAudio()
}

// Active 返回有活跃流的设备 serial 列表。
func (m *Manager) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.streams))
	for serial := range m.streams {
		out = append(out, serial)
	}
	return out
}
