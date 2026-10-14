package scrcpy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// server 设备端文件路径（scrcpy 约定路径）。
const (
	serverDevicePath = "/data/local/tmp/scrcpy-server.jar"
	dialAttempts     = 100 // forward 模式重连次数（与 scrcpy 一致）
	dialInterval     = 100 * time.Millisecond
	handshakeTimeout = 10 * time.Second       // 设备名/元数据读取上限
	acceptTimeout    = 15 * time.Second       // reverse 模式等待设备回连上限
	outputWaitOnFail = 500 * time.Millisecond // 失败时等待 server 错误输出到达
	outputCapSize    = 8 * 1024
)

// StreamInfo 是投屏流建立后的元信息。
type StreamInfo struct {
	Serial     string `json:"serial"`
	DeviceName string `json:"device"`
	Codec      string `json:"codec"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// Stream 是一台设备上的一条投屏流：负责 server 生命周期、
// 隧道建立、协议解析与扇出。
type Stream struct {
	info StreamInfo
	hub  *Hub

	videoConn   net.Conn
	controlConn net.Conn
	controlMu   sync.Mutex
	shellConn   net.Conn
	adb         streamDeps

	// 剪贴板（设备端下行 DeviceMessage 更新）
	clipboardMu  sync.Mutex
	clipboard    string
	clipboardSeq uint64
	clipboardNtf chan struct{}

	// 隧道信息（停止时清理）
	tunnelMode string // "reverse" | "forward"
	tunnelPort int
	socketName string

	stopOnce sync.Once
}

// streamDeps 抽象 adb 操作，便于测试与解耦。
type streamDeps interface {
	Push(serial, remotePath string, data []byte, mode string) error
	Forward(serial, local, remote string) error
	KillForward(serial, local string) error
	Reverse(serial, remote, local string) error
	KillReverse(serial, remote string) error
	ShellConn(serial, cmd string) (net.Conn, error)
}

// outputCapture 环形捕获 server 的 shell 输出，失败时带回诊断信息。
type outputCapture struct {
	mu  sync.Mutex
	buf []byte
}

func newOutputCapture() *outputCapture { return &outputCapture{} }

func (c *outputCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if len(c.buf) > outputCapSize {
		c.buf = c.buf[len(c.buf)-outputCapSize:]
	}
	return len(p), nil
}

func (c *outputCapture) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// start 建立一条完整投屏链路：
// push jar → 建隧道（reverse 优先，forward 回退）→ 启动 server → 握手 → 读循环。
// 执行成功时流已在后台运行；任何一步失败都会清理现场并带回 server 输出。
func start(deps streamDeps, serial string, jarBytes []byte, serverVersion string, opts Options) (*Stream, error) {
	s := &Stream{hub: NewHub(), adb: deps, info: StreamInfo{Serial: serial}, clipboardNtf: make(chan struct{}, 1)}

	// 1. 推送 server jar
	if err := deps.Push(serial, serverDevicePath, jarBytes, "0644"); err != nil {
		return nil, fmt.Errorf("推送 scrcpy-server 失败: %w", err)
	}

	// 2. 建立隧道并启动 server
	scid := uint32(time.Now().UnixNano()) & 0x7FFFFFFF
	s.socketName = fmt.Sprintf("scrcpy_%08x", scid)
	capture := newOutputCapture()

	videoConn, err := s.openReverse(deps, scid, serverVersion, opts, capture)
	if err != nil {
		log.Printf("[scrcpy:%s] reverse 隧道不可用，回退 forward: %v", serial, err)
		videoConn, err = s.openForward(deps, scid, serverVersion, opts, capture)
	}
	if err != nil {
		time.Sleep(outputWaitOnFail) // 等 server 的错误输出到达
		return nil, fmt.Errorf("%v\nserver 输出: %s", err, capture.snapshot())
	}
	s.videoConn = videoConn

	// 3. 握手：64 字节设备名 + 12 字节视频元数据
	// （reverse 模式无 dummy 字节；forward 模式的 dummy 已在 openForward 内消费）
	_ = videoConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var name [64]byte
	if _, err := io.ReadFull(videoConn, name[:]); err != nil {
		s.Stop()
		return nil, fmt.Errorf("读取设备名失败: %w", err)
	}
	var meta [12]byte
	if _, err := io.ReadFull(videoConn, meta[:]); err != nil {
		s.Stop()
		return nil, fmt.Errorf("读取视频元数据失败: %w", err)
	}
	_ = videoConn.SetReadDeadline(time.Time{}) // 长连接，清除超时

	if codecID := binary.BigEndian.Uint32(meta[0:4]); codecID != 0x68323634 { // "h264"
		s.Stop()
		return nil, fmt.Errorf("意外的视频编码: 0x%08x", codecID)
	}
	s.info = StreamInfo{
		Serial:     serial,
		DeviceName: trimName(name[:]),
		Codec:      "h264",
		Width:      int(binary.BigEndian.Uint32(meta[4:8])),
		Height:     int(binary.BigEndian.Uint32(meta[8:12])),
	}

	// 4. server 进程退出（设备断开/崩溃）→ 记录输出并结束流
	go func() {
		_, _ = io.Copy(capture, s.shellConn)
		if out := capture.snapshot(); out != "" {
			log.Printf("[scrcpy:%s] server 输出: %s", serial, out)
		}
		s.Stop()
	}()

	// 5. 控制连接下行：解析设备消息（剪贴板等）
	go s.deviceMessageLoop()

	// 6. 读循环
	go s.readLoop()
	return s, nil
}

// deviceMessageLoop 解析控制 socket 的下行 DeviceMessage（scrcpy v2.4 格式）：
//   - CLIPBOARD(0):    type 1B + text len u32 BE + utf8
//   - ACK_CLIPBOARD(1): type 1B + sequence u64 BE
//   - UHID_OUTPUT(2):  type 1B + id u16 + len u16 + data（忽略）
func (s *Stream) deviceMessageLoop() {
	defer s.Stop()
	conn := s.controlConn
	for {
		var t [1]byte
		if _, err := io.ReadFull(conn, t[:]); err != nil {
			if !isNetClosed(err) {
				log.Printf("[scrcpy:%s] 设备消息读取结束: %v", s.info.Serial, err)
			}
			return
		}
		switch t[0] {
		case 0: // CLIPBOARD
			var l [4]byte
			if _, err := io.ReadFull(conn, l[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(l[:])
			text := make([]byte, n)
			if _, err := io.ReadFull(conn, text); err != nil {
				return
			}
			s.setClipboard(string(text))
		case 1: // ACK_CLIPBOARD
			var seq [8]byte
			if _, err := io.ReadFull(conn, seq[:]); err != nil {
				return
			}
			s.setClipboardSeq(binary.BigEndian.Uint64(seq[:]))
		case 2: // UHID_OUTPUT：跳过
			var h [4]byte
			if _, err := io.ReadFull(conn, h[:]); err != nil {
				return
			}
			n := binary.BigEndian.Uint16(h[2:])
			if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
				return
			}
		default:
			log.Printf("[scrcpy:%s] 未知设备消息类型 %d", s.info.Serial, t[0])
			return
		}
	}
}

func (s *Stream) setClipboard(text string) {
	s.clipboardMu.Lock()
	s.clipboard = text
	s.clipboardMu.Unlock()
	select {
	case s.clipboardNtf <- struct{}{}:
	default:
	}
}

func (s *Stream) setClipboardSeq(seq uint64) {
	s.clipboardMu.Lock()
	s.clipboardSeq = seq
	s.clipboardMu.Unlock()
}

// Clipboard 返回最近一次设备剪贴板内容。
func (s *Stream) Clipboard() string {
	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	return s.clipboard
}

// WaitClipboard 等待新的剪贴板内容到达（超时返回当前值）。
func (s *Stream) WaitClipboard(timeout time.Duration) string {
	select {
	case <-s.clipboardNtf:
	case <-time.After(timeout):
	}
	return s.Clipboard()
}

// openReverse 使用 adb reverse（scrcpy 默认隧道方式）：
// 本机监听 → adb reverse → 启动 server → 设备主动回连，无连接竞态。
func (s *Stream) openReverse(deps streamDeps, scid uint32, serverVersion string, opts Options, capture *outputCapture) (net.Conn, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听本地端口失败: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.tunnelMode, s.tunnelPort = "reverse", port

	if err := deps.Reverse(s.info.Serial, "localabstract:"+s.socketName, fmt.Sprintf("tcp:%d", port)); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("建立 adb reverse 失败: %w", err)
	}

	// tunnel_forward=false（默认），server 主动连出
	if err := s.launchServer(deps, scid, serverVersion, opts, false, capture); err != nil {
		s.Stop()
		return nil, err
	}

	// Accept 无原生超时，用定时器关闭 listener 来解除阻塞
	timer := time.AfterFunc(acceptTimeout, func() { _ = ln.Close() })
	conn, err := ln.Accept()
	timer.Stop()
	if err != nil {
		_ = ln.Close()
		s.Stop()
		return nil, fmt.Errorf("等待设备回连超时: %w", err)
	}

	// control socket（reverse 模式下 server 依次连出：video → control）
	if ctl, err := acceptControl(ln); err != nil {
		_ = ln.Close()
		s.Stop()
		return nil, fmt.Errorf("等待控制连接超时: %w", err)
	} else {
		s.controlConn = ctl
	}
	_ = ln.Close() // 连接全部建立，关闭 listener
	return conn, nil
}

// acceptControl 在 listener 上接受控制连接。
func acceptControl(ln net.Listener) (net.Conn, error) {
	timer := time.AfterFunc(acceptTimeout, func() { _ = ln.Close() })
	defer timer.Stop()
	return ln.Accept()
}

// openForward 使用 adb forward（回退方式）：本机主动连接，
// server 未就绪时按 scrcpy 策略"连接+dummy 读取"整体重试。
func (s *Stream) openForward(deps streamDeps, scid uint32, serverVersion string, opts Options, capture *outputCapture) (net.Conn, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("分配本地端口失败: %w", err)
	}
	s.tunnelMode, s.tunnelPort = "forward", port

	if err := deps.Forward(s.info.Serial, fmt.Sprintf("tcp:%d", port), "localabstract:"+s.socketName); err != nil {
		return nil, fmt.Errorf("建立 adb forward 失败: %w", err)
	}

	// tunnel_forward=true，server 监听等待连入
	if err := s.launchServer(deps, scid, serverVersion, opts, true, capture); err != nil {
		s.Stop()
		return nil, err
	}

	conn, err := connectWithDummy(s.tunnelPort, dialAttempts, dialInterval)
	if err != nil {
		s.Stop()
		return nil, err
	}

	// control socket：forward 模式下 server 在 accept 视频后等待第二个连接
	// （dummy 字节只在第一个 socket 上发送，control 无）
	ctl, err := dialControl(s.tunnelPort, dialAttempts, dialInterval)
	if err != nil {
		s.Stop()
		return nil, err
	}
	s.controlConn = ctl
	return conn, nil
}

// dialControl 连接 forward 隧道的控制 socket。
func dialControl(port int, attempts int, interval time.Duration) (net.Conn, error) {
	var lastErr error
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < attempts; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(interval)
	}
	return nil, fmt.Errorf("control 连接重试 %d 次后仍失败: %v", attempts, lastErr)
}

// launchServer 通过 adb shell 启动设备端 server（长驻进程，连接保活期间运行）。
func (s *Stream) launchServer(deps streamDeps, scid uint32, serverVersion string, opts Options, tunnelForward bool, capture *outputCapture) error {
	tf := ""
	if tunnelForward {
		tf = " tunnel_forward=true"
	}
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.genymobile.scrcpy.Server %s "+
		"scid=%08x%s log_level=warn video_codec=%s max_size=%d video_bit_rate=%d max_fps=%d "+
		"audio=false control=true clipboard_autosync=false cleanup=false",
		serverDevicePath, serverVersion, scid, tf, opts.VideoCodec,
		opts.MaxSize, opts.BitRate, opts.MaxFPS)
	shellConn, err := deps.ShellConn(s.info.Serial, cmd)
	if err != nil {
		return fmt.Errorf("启动 scrcpy server 失败: %w", err)
	}
	s.shellConn = shellConn
	return nil
}

// Info 返回流元信息。
func (s *Stream) Info() StreamInfo { return s.info }

// Subscribe 注册一个视频订阅者，返回订阅者、取消函数与缓存的配置包。
func (s *Stream) Subscribe() (*Subscriber, func(), []byte) {
	return s.hub.Add()
}

// Stop 结束流（幂等）：关闭连接与 hub，清理隧道。
func (s *Stream) Stop() {
	s.stopOnce.Do(func() {
		if s.videoConn != nil {
			_ = s.videoConn.Close()
		}
		if s.controlConn != nil {
			_ = s.controlConn.Close()
		}
		if s.shellConn != nil {
			_ = s.shellConn.Close()
		}
		s.shutdownTunnel()
		s.hub.Close()
		log.Printf("[scrcpy:%s] 流已停止", s.info.Serial)
	})
}

// SendControl 向设备写入一条 ControlMessage（并发安全）。
func (s *Stream) SendControl(msg []byte) error {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	_, err := s.controlConn.Write(msg)
	return err
}

// VideoSize 返回当前视频尺寸（触摸注入的 screenW/H 必须与之匹配）。
func (s *Stream) VideoSize() (int, int) {
	return s.info.Width, s.info.Height
}

func (s *Stream) shutdownTunnel() {
	switch s.tunnelMode {
	case "reverse":
		_ = s.adb.KillReverse(s.info.Serial, "localabstract:"+s.socketName)
	case "forward":
		_ = s.adb.KillForward(s.info.Serial, fmt.Sprintf("tcp:%d", s.tunnelPort))
	}
}

// readLoop 解析帧头并广播；出错即结束流。
func (s *Stream) readLoop() {
	defer s.Stop()
	conn := s.videoConn
	var hdr [12]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			if !isNetClosed(err) {
				log.Printf("[scrcpy:%s] 读帧头结束: %v", s.info.Serial, err)
			}
			return
		}
		ptsFlags := binary.BigEndian.Uint64(hdr[0:8])
		size := binary.BigEndian.Uint32(hdr[8:12])
		data := make([]byte, size)
		if _, err := io.ReadFull(conn, data); err != nil {
			log.Printf("[scrcpy:%s] 读帧数据结束: %v", s.info.Serial, err)
			return
		}
		s.hub.Broadcast(Packet{
			Kind:   PacketVideo,
			Config: ptsFlags&(1<<63) != 0,
			Key:    ptsFlags&(1<<62) != 0,
			PTS:    ptsFlags &^ (1<<63 | 1<<62),
			Data:   data,
		})

		// 配置包携带 SPS：旋转/分辨率变化时解析新尺寸并广播 resize 事件
		if ptsFlags&(1<<63) != 0 {
			if w, h, ok := parseSPSResolution(findNAL(data, 7)); ok && (w != s.info.Width || h != s.info.Height) {
				s.info.Width, s.info.Height = w, h
				log.Printf("[scrcpy:%s] 视频尺寸变化: %dx%d", s.info.Serial, w, h)
				s.hub.Broadcast(Packet{
					Kind: PacketResize,
					Data: []byte(fmt.Sprintf(`{"width":%d,"height":%d}`, w, h)),
				})
			}
		}
	}
}

// connectWithDummy 连接隧道并等待 dummy 字节，任一失败即重试
// （与 scrcpy connect_to_server 策略一致，server 就绪前 forward 连接会被断开）。
func connectWithDummy(port int, attempts int, interval time.Duration) (net.Conn, error) {
	var lastErr error
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < attempts; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			var dummy [1]byte
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err = io.ReadFull(conn, dummy[:])
			if err == nil {
				_ = conn.SetReadDeadline(time.Time{})
				return conn, nil
			}
			_ = conn.Close() // server 未就绪，稍后重连
		}
		lastErr = err
		time.Sleep(interval)
	}
	return nil, fmt.Errorf("重试 %d 次后仍失败: %v", attempts, lastErr)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func trimName(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func isNetClosed(err error) bool {
	return err == net.ErrClosed
}
