package scrcpy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// server 设备端文件路径（scrcpy 约定路径）。
const (
	serverDevicePath   = "/data/local/tmp/scrcpy-server.jar"
	dialAttempts       = 100 // forward 模式重连次数（与 scrcpy 一致）
	dialInterval       = 100 * time.Millisecond
	handshakeTimeout   = 10 * time.Second       // 设备名/元数据读取上限
	audioHeaderTimeout = 60 * time.Second       // 音频编码 ID 读取上限（采集初始化慢）
	acceptTimeout      = 15 * time.Second       // reverse 模式等待设备回连上限
	outputWaitOnFail   = 500 * time.Millisecond // 失败时等待 server 错误输出到达
	outputCapSize      = 8 * 1024
)

// StreamInfo 是投屏流建立后的元信息。
type StreamInfo struct {
	Serial     string `json:"serial"`
	DeviceName string `json:"device"`
	Codec      string `json:"codec"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Audio      bool   `json:"audio"` // 音频流是否可用
}

// Stream 是一台设备上的一条投屏流：负责 server 生命周期、
// 隧道建立、协议解析与扇出。
type Stream struct {
	info StreamInfo
	hub  *Hub

	videoConn   net.Conn
	audioConn   net.Conn
	controlConn net.Conn
	controlMu   sync.Mutex
	shellConn   net.Conn
	adb         streamDeps

	// 音频流（scrcpy v2 audio，独立于视频扇出）
	audioHub *Hub

	// 音频已被设备端明确禁用（编码 ID 0/错误码/读取失败），
	// 用于让音频订阅方立即失败而非无限重试
	audioDisabled atomic.Bool

	// 音频 socket 是否已建立（隧道建立时即确定；设备采集头可能还要 1~2 秒才到）。
	// WebRTC 挂音频轨以此为据，无需等 setupAudioAsync 读完编码 ID——等它会把
	// answer 拖到关键帧窗口之后，视频首帧被无谓推迟。
	audioSocket atomic.Bool

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
	Shell(serial, cmd string) (string, error)
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
	s := &Stream{hub: NewHub(), audioHub: NewHub(), adb: deps, info: StreamInfo{Serial: serial}, clipboardNtf: make(chan struct{}, 1)}

	// 1. 推送 server jar
	if err := deps.Push(serial, serverDevicePath, jarBytes, "0644"); err != nil {
		return nil, fmt.Errorf("推送 scrcpy-server 失败: %w", err)
	}

	// 1.5 唤醒屏幕：熄屏时设备端编码器没有输入帧，GOP 内（最长 10 秒）无关键帧
	// 可供新订阅者解码，表现为启动后长时间黑屏
	if _, err := deps.Shell(serial, "input keyevent KEYCODE_WAKEUP"); err != nil {
		log.Printf("[scrcpy:%s] 唤醒屏幕失败: %v", serial, err)
	}

	// 1.6 需要软解（h264bsd 只吃 Baseline）时，探测设备端软件编码器并按名指定：
	// 硬编码器普遍输出 High，软解啃不动；软件编码器只输出 Baseline，是唯一可靠路径。
	if opts.PreferSoftwareEncoder && opts.VideoEncoder == "" {
		if name := probeSoftwareEncoder(deps, serial, serverVersion); name != "" {
			opts.VideoEncoder = name
			log.Printf("[scrcpy:%s] 软解模式：改用设备端软件编码器 %s", serial, name)
		} else {
			log.Printf("[scrcpy:%s] 软解模式：未探测到软件 H.264 编码器，仍由设备自选（软解可能失败）", serial)
		}
	}

	// 2. 建立隧道并启动 server。
	// 启用音频时直接用 forward 模式：reverse 的多连接转发在部分平台
	// （Windows adb server 实测）会丢失第 3 个（control）连接；
	// 纯视频时 reverse 优先（无连接竞态），失败回退 forward。
	scid := uint32(time.Now().UnixNano()) & 0x7FFFFFFF
	s.socketName = fmt.Sprintf("scrcpy_%08x", scid)
	capture := newOutputCapture()

	var videoConn net.Conn
	var err error
	if !audioEnabled(opts) {
		videoConn, err = s.openReverse(deps, scid, serverVersion, opts, capture)
		if err != nil {
			log.Printf("[scrcpy:%s] reverse 隧道不可用，回退 forward: %v", serial, err)
		}
	}
	if videoConn == nil {
		videoConn, err = s.openForward(deps, scid, serverVersion, opts, capture)
	}
	if err != nil {
		time.Sleep(outputWaitOnFail) // 等 server 的错误输出到达
		return nil, fmt.Errorf("%v\nserver 输出: %s", err, capture.snapshot())
	}
	s.videoConn = videoConn

	// 3. 握手（v5）：64 字节设备名 + 4 字节编码 ID + 12 字节会话元数据（SESSION flag/width/height）
	// （reverse 模式无 dummy 字节；forward 模式的 dummy 已在 openForward 内消费）
	_ = videoConn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var name [64]byte
	if _, err := io.ReadFull(videoConn, name[:]); err != nil {
		s.Stop()
		return nil, fmt.Errorf("读取设备名失败: %w", err)
	}
	var codec [4]byte
	if _, err := io.ReadFull(videoConn, codec[:]); err != nil {
		s.Stop()
		return nil, fmt.Errorf("读取视频编码 ID 失败: %w", err)
	}
	var sessionMeta [12]byte
	if _, err := io.ReadFull(videoConn, sessionMeta[:]); err != nil {
		s.Stop()
		return nil, fmt.Errorf("读取会话元数据失败: %w", err)
	}
	_ = videoConn.SetReadDeadline(time.Time{}) // 长连接，清除超时

	if codecID := binary.BigEndian.Uint32(codec[0:4]); codecID != 0x68323634 { // "h264"
		s.Stop()
		return nil, fmt.Errorf("意外的视频编码: 0x%08x", codecID)
	}
	s.info = StreamInfo{
		Serial:     serial,
		DeviceName: trimName(name[:]),
		Codec:      "h264",
		Width:      int(binary.BigEndian.Uint32(sessionMeta[4:8])),
		Height:     int(binary.BigEndian.Uint32(sessionMeta[8:12])),
	}

	// 4. shell 输出消费必须先于一切阻塞读取：adbd 缓冲写满会阻塞
	//    设备端 server（音频初始化期间卡死的根因）
	go func() {
		_, _ = io.Copy(capture, s.shellConn)
		if out := capture.snapshot(); out != "" {
			log.Printf("[scrcpy:%s] server 输出: %s", serial, out)
		}
		s.Stop()
	}()

	// 音频 socket 建立即可判定「本流带音频」：设备采集头可能还要 1~2 秒才到，
	// 但 WebRTC 挂音频轨不需要等它（answer 不能堵在关键帧窗口之前）
	if s.audioConn != nil {
		s.audioSocket.Store(true)
	}

	// 5. 音频 setup 异步进行（音频采集初始化可能很慢，绝不阻塞视频出画）；
	//    视频/控制读循环立即启动
	go s.deviceMessageLoop()
	go s.readLoop()
	go s.setupAudioAsync()
	return s, nil
}

// setupAudioAsync 异步读取音频头（audio socket 已按 video → audio → control
// 顺序建立）：4 字节编码 ID；设备端禁用音频写入 0（继续仅视频）或 1（配置错误）。
// 音频采集初始化可能较慢，超时放宽到 60s；失败一律降级为仅视频。
func (s *Stream) setupAudioAsync() {
	if s.audioConn == nil {
		return
	}
	_ = s.audioConn.SetReadDeadline(time.Now().Add(audioHeaderTimeout))
	var codec [4]byte
	if _, err := io.ReadFull(s.audioConn, codec[:]); err != nil {
		log.Printf("[scrcpy:%s] 音频头读取失败，降级为仅视频: %v", s.info.Serial, err)
		s.audioDisabled.Store(true)
		_ = s.audioConn.Close()
		s.audioConn = nil
		return
	}
	_ = s.audioConn.SetReadDeadline(time.Time{})
	switch id := binary.BigEndian.Uint32(codec[:]); id {
	case 0x6F707573: // "opus"
		s.info.Audio = true
		log.Printf("[scrcpy:%s] 音频流就绪 (opus)", s.info.Serial)
		s.audioReadLoop() // 阻塞消费音频流
	case 0:
		log.Printf("[scrcpy:%s] 设备端禁用了音频，继续仅视频", s.info.Serial)
		s.audioDisabled.Store(true)
	default:
		log.Printf("[scrcpy:%s] 音频不可用（编码 ID 0x%08x），继续仅视频", s.info.Serial, id)
		s.audioDisabled.Store(true)
	}
}

// audioReadLoop 解析音频帧（帧头与视频相同：u64 flags+PTS / u32 size）并广播；
// 配置包为 OpusHead。出错仅结束音频（视频继续）。
func (s *Stream) audioReadLoop() {
	defer s.audioHub.Close()
	conn := s.audioConn
	var hdr [12]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			if !isNetClosed(err) {
				log.Printf("[scrcpy:%s] 音频流读取结束: %v", s.info.Serial, err)
			}
			return
		}
		ptsFlags := binary.BigEndian.Uint64(hdr[0:8])
		size := binary.BigEndian.Uint32(hdr[8:12])
		data := make([]byte, size)
		if _, err := io.ReadFull(conn, data); err != nil {
			log.Printf("[scrcpy:%s] 音频数据读取结束: %v", s.info.Serial, err)
			return
		}
		// v5 音频帧头：bit62=config、bit61=key、低 61 位 PTS（音频流无 SESSION 包）
		s.audioHub.Broadcast(Packet{
			Kind:   PacketVideo,
			Config: ptsFlags&(1<<62) != 0,
			Key:    ptsFlags&(1<<61) != 0,
			PTS:    ptsFlags &^ (1<<63 | 1<<62 | 1<<61),
			Data:   data,
		})
	}
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
	for i := 1; ; i++ {
		conn, err := ln.Accept()
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("等待设备回连超时: %w", err)
		}
		switch i {
		case 1:
			s.videoConn = conn
		case 2:
			if audioEnabled(opts) {
				s.audioConn = conn
			} else {
				s.controlConn = conn
			}
		default:
			s.controlConn = conn
		}
		if (audioEnabled(opts) && i >= 3) || (!audioEnabled(opts) && i >= 2) {
			break
		}
	}
	timer.Stop()
	_ = ln.Close() // 连接全部建立，关闭 listener
	return s.videoConn, nil
}

// acceptSocket 在 listener 上接受一条连接（带超时）。
func acceptSocket(ln net.Listener) (net.Conn, error) {
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

	// tunnel_forward=true，server 监听等待连入。
	// connectWithDummy 收到 dummy 即代表 server 已监听并 accept 了 video，
	// 其后的 audio/control dial 必然命中同一个 LocalServerSocket，无需盲等。
	if err := s.launchServer(deps, scid, serverVersion, opts, true, capture); err != nil {
		s.Stop()
		return nil, err
	}

	conn, err := connectWithDummy(s.tunnelPort, dialAttempts, dialInterval)
	if err != nil {
		s.Stop()
		return nil, err
	}

	// server 依次 accept：video → audio（可选）→ control
	// （dummy 字节只在第一个 socket 上发送，后续无）
	if audioEnabled(opts) {
		audio, err := dialSocket(s.tunnelPort, dialAttempts, dialInterval)
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("audio 连接失败: %w", err)
		}
		s.audioConn = audio
	}
	ctl, err := dialSocket(s.tunnelPort, dialAttempts, dialInterval)
	if err != nil {
		s.Stop()
		return nil, fmt.Errorf("control 连接失败: %w", err)
	}
	s.controlConn = ctl
	return conn, nil
}

// dialSocket 连接 forward 隧道的一条 socket。
func dialSocket(port int, attempts int, interval time.Duration) (net.Conn, error) {
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
	return nil, fmt.Errorf("重试 %d 次后仍失败: %v", attempts, lastErr)
}

// audioEnabled 返回是否启用音频转发。
func audioEnabled(o Options) bool {
	return o.Audio != nil && *o.Audio
}

// launchServer 通过 adb shell 启动设备端 server（长驻进程，连接保活期间运行）。
func (s *Stream) launchServer(deps streamDeps, scid uint32, serverVersion string, opts Options, tunnelForward bool, capture *outputCapture) error {
	tf := ""
	if tunnelForward {
		tf = " tunnel_forward=true"
	}
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.genymobile.scrcpy.Server %s "+
		"scid=%08x%s log_level=warn video_codec=%s max_size=%d video_bit_rate=%d max_fps=%d "+
		"video_codec_options=i-frame-interval=2 "+
		"audio=%v audio_codec=opus clipboard_autosync=false stay_awake=true cleanup=false",
		serverDevicePath, serverVersion, scid, tf, opts.VideoCodec,
		opts.MaxSize, opts.BitRate, opts.MaxFPS, audioEnabled(opts))
	if opts.VideoEncoder != "" { // 软解模式：指定软件编码器以强制 Baseline 档
		cmd += " video_encoder=" + opts.VideoEncoder
	}
	if opts.AudioDup { // 两边都出声：回放捕获 + 设备扬声器继续播放（v5，Android 13+）
		cmd += " audio_source=playback audio_dup=true"
	}
	shellConn, err := deps.ShellConn(s.info.Serial, cmd)
	if err != nil {
		return fmt.Errorf("启动 scrcpy server 失败: %w", err)
	}
	log.Printf("[scrcpy:%s] 启动命令: %s", s.info.Serial, cmd)
	s.shellConn = shellConn
	return nil
}

// Info 返回流元信息。
func (s *Stream) Info() StreamInfo { return s.info }

// probeSoftwareEncoder 通过 server 的 list_encoders 列出设备编码器，
// 返回可用的软件 H.264 编码器名（无则空串）。软件编码器只输出 Baseline 档，
// 是浏览器端 h264bsd 软解唯一能接受的档位；名字随 ROM 而异，故运行时探测而非硬编码。
func probeSoftwareEncoder(deps streamDeps, serial, serverVersion string) string {
	// 必须带 cleanup=false：list_encoders 模式打印完列表就退出，默认 cleanup=true 会顺手
	// 删掉设备上的 server jar，让紧接其后的正式启动以 ClassNotFoundException 崩溃。
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.genymobile.scrcpy.Server %s list_encoders=true cleanup=false",
		serverDevicePath, serverVersion)
	out, err := deps.Shell(serial, cmd)
	if err != nil {
		log.Printf("[scrcpy:%s] 列出编码器失败: %v", serial, err)
		return ""
	}
	// 输出形如：    --video-codec=h264 --video-encoder=c2.android.avc.encoder         (sw)
	re := regexp.MustCompile(`--video-codec=h264\s+--video-encoder=(\S+)\s+\(sw\)`)
	if m := re.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// Viewers 返回当前视频观看者数量（每个观看页占一个订阅）。
func (s *Stream) Viewers() int { return s.hub.Count() }

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
		if s.audioConn != nil {
			_ = s.audioConn.Close()
		}
		if s.controlConn != nil {
			_ = s.controlConn.Close()
		}
		s.shutdownTunnel()
		s.hub.Close()
		s.audioHub.Close()
		log.Printf("[scrcpy:%s] 流已停止", s.info.Serial)
	})
}

// SubscribeAudio 订阅音频流（Opus）。若音频不可用返回错误。
func (s *Stream) SubscribeAudio() (*Subscriber, func(), []byte, error) {
	if !s.info.Audio {
		return nil, nil, nil, fmt.Errorf("该流没有音频")
	}
	sub, cancel, cfg := s.audioHub.Add()
	return sub, cancel, cfg, nil
}

// AudioDisabled 报告设备端是否已明确禁用音频（区别于「尚未就绪」）。
func (s *Stream) AudioDisabled() bool { return s.audioDisabled.Load() }

// AudioAttachable 报告音频 socket 是否已建立（采集就绪前即可挂 WebRTC 音频轨）。
func (s *Stream) AudioAttachable() bool { return s.audioSocket.Load() }

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
		// v5 帧头：bit63=SESSION(会话元数据包，整个 12B 即内容、无载荷)、bit62=config、bit61=key、低 61 位 PTS
		if ptsFlags&(1<<63) != 0 {
			continue // 会话元数据（flags/width/height），尺寸已由 SPS 解析覆盖
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(conn, data); err != nil {
			log.Printf("[scrcpy:%s] 读帧数据结束: %v", s.info.Serial, err)
			return
		}
		s.hub.Broadcast(Packet{
			Kind:     PacketVideo,
			Config:   ptsFlags&(1<<62) != 0,
			Key:      ptsFlags&(1<<61) != 0,
			PTS:      ptsFlags &^ (1<<63 | 1<<62 | 1<<61),
			RecvWall: time.Now().UnixMicro(),
			Data:     data,
		})

		// 配置包携带 SPS：旋转/分辨率变化时解析新尺寸并广播 resize 事件
		if ptsFlags&(1<<62) != 0 {
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
