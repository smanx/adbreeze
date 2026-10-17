// Package scrcpy 实现投屏链路：推送并启动设备端 scrcpy-server，
// 建立视频流隧道，解析 scrcpy client 协议并扇出给多个 WebSocket 订阅者。
//
// 协议依据 scrcpy v5.0.1（与内嵌 server jar 版本严格配套）：
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
	AudioDup bool // audio_dup（v5，需 audio_source=playback，Android 13+）：采集的同时设备扬声器也出声

	// VideoEncoder 指定设备端视频编码器名（如 c2.android.avc.encoder）；空则由设备自选（通常为硬编）。
	// 调用方一般不直接填，需要软解能吃的 Baseline 码流时置 PreferSoftwareEncoder 由 start() 探测回填。
	VideoEncoder string
	// PreferSoftwareEncoder 为 true 时改用设备端软件 H.264 编码器：它只输出 Baseline 档，
	// 是浏览器端 h264bsd 软解唯一能接受的档位（硬编码器普遍输出 High，软解啃不动）。
	PreferSoftwareEncoder bool
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

	// 已有流时通常直接复用（画质等参数以先到者为准，多观众共享同一条流）。
	// 唯一必须满足的是软解需求：WASM 软解（h264bsd）只吃 Baseline 档，而硬编输出
	// High——若请求方要软编而现有流是硬编，复用等于让请求方永远解不出画面
	// （失败遮罩盖住画布，触控也全部落在遮罩上）。此时重启设备端流。
	restart := false
	m.mu.Lock()
	if s, ok := m.streams[serial]; ok {
		info := s.Info()
		if opts.PreferSoftwareEncoder && s.opts.VideoEncoder == "" {
			log.Printf("[scrcpy:%s] 新请求需要软件编码器（软解），重启现有硬编流", serial)
			restart = true
		} else {
			m.mu.Unlock()
			return info, nil
		}
	}
	m.mu.Unlock()
	if restart {
		_ = m.Stop(serial) // 停掉硬编流，落到下面按新参数重启
	}

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

// Info 返回设备当前流的元信息（不订阅）。
func (m *Manager) Info(serial string) (StreamInfo, error) {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return StreamInfo{}, fmt.Errorf("设备 %s 没有活跃的投屏流", serial)
	}
	return s.Info(), nil
}

// Viewers 返回设备当前流的观看者数量（流不存在时返回 0）。
func (m *Manager) Viewers(serial string) int {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	if !ok {
		return 0
	}
	return s.Viewers()
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

// AudioAttachable 报告流的音频 socket 是否已建立（采集就绪前即可挂音频轨）。
// 设备音频采集头通常还要 1~2 秒才到，但 WebRTC 的 answer 不能等它——视频
// 首帧优先，推流协程后台等订阅就绪即可。
func (m *Manager) AudioAttachable(serial string) bool {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	return ok && s.AudioAttachable()
}

// AudioDisabled 报告设备端是否已明确禁用音频（流不存在时返回 false）。
func (m *Manager) AudioDisabled(serial string) bool {
	m.mu.Lock()
	s, ok := m.streams[serial]
	m.mu.Unlock()
	return ok && s.AudioDisabled()
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
