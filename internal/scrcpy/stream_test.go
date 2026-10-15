package scrcpy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeDeps 模拟 adb 层：Forward 时在本机起一个"假设备"，
// 按 scrcpy v2.4 协议完成握手并推送测试帧。
type fakeDeps struct {
	framesReady chan struct{} // 关闭后假设备才开始发帧
	withAudio   bool          // 假设备提供音频流（video → audio → control）
	audioReady  chan struct{} // 关闭后假设备才开始发音频帧
	ln          net.Listener
	shellEnd    net.Conn
}

func (f *fakeDeps) Push(serial, remote string, data []byte, mode string) error { return nil }

func (f *fakeDeps) Shell(serial, cmd string) (string, error) { return "", nil }

func (f *fakeDeps) Forward(serial, local, remote string) error {
	port, err := strconv.Atoi(strings.TrimPrefix(local, "tcp:"))
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	f.ln = ln
	go f.serve()
	return nil
}

func (f *fakeDeps) KillForward(serial, local string) error { return nil }

// Reverse 故意失败，让 start() 走 forward 回退路径（与假设备的 listen 模型匹配）。
func (f *fakeDeps) Reverse(serial, remote, local string) error {
	return fmt.Errorf("fake: reverse not supported")
}

func (f *fakeDeps) KillReverse(serial, remote string) error { return nil }

func (f *fakeDeps) ShellConn(serial, cmd string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	f.shellEnd = c2
	go io.Copy(io.Discard, c1) // 保持连接打开并吞掉输出，模拟长驻进程
	return c1, nil
}

func (f *fakeDeps) serve() {
	// 客户端按 video → audio（可选）→ control 顺序拨号，真实 server 也严格按
	// accept 顺序分配角色。多个 goroutine 并发 Accept 会让第 2/3 条连接错分到
	// audio/control（Linux CI 实测复现），因此统一由单个 accept 循环按序派发。
	next := make(chan net.Conn, 3)
	go func() {
		defer close(next)
		for {
			c, err := f.ln.Accept()
			if err != nil {
				return
			}
			next <- c
		}
	}()

	video, ok := <-next
	if !ok {
		return
	}
	go f.serveVideo(video) // 需并发发出 dummy 字节，否则客户端不会拨号下一条

	if f.withAudio {
		if audio, ok := <-next; ok {
			go f.serveAudio(audio)
		}
	}
	if ctl, ok := <-next; ok {
		go io.Copy(io.Discard, ctl)
	}
}

// serveAudio 推送音频流：4B 编码 ID "opus" + OpusHead 配置包 + 音频帧。
func (f *fakeDeps) serveAudio(conn net.Conn) {
	defer conn.Close()
	if _, err := conn.Write([]byte{0x6f, 0x70, 0x75, 0x73}); err != nil { // "opus"
		return
	}
	if f.audioReady != nil {
		<-f.audioReady
	}
	opusHead := append([]byte("OpusHead"), []byte{1, 2, 0x38, 0x01, 0x80, 0xBB, 0x00, 0x00, 0, 0, 0}...)
	writeFrame(conn, true, false, 0, opusHead)                     // 配置包
	writeFrame(conn, false, false, 1000, []byte{0xFC, 0xFF, 0xFE}) // 音频帧
	time.Sleep(3 * time.Second)
}

func (f *fakeDeps) serveVideo(conn net.Conn) {
	defer conn.Close()
	// 握手（v5）：dummy + 64B 设备名 + 4B codecId + 12B 会话元数据
	if _, err := conn.Write([]byte{0}); err != nil {
		return
	}
	name := make([]byte, 64)
	copy(name, "FakeDevice")
	if _, err := conn.Write(name); err != nil {
		return
	}
	codec := make([]byte, 4)
	binary.BigEndian.PutUint32(codec, 0x68323634) // "h264"
	if _, err := conn.Write(codec); err != nil {
		return
	}
	sessionMeta := make([]byte, 12)
	binary.BigEndian.PutUint32(sessionMeta[0:4], 0x80000000) // SESSION 标志
	binary.BigEndian.PutUint32(sessionMeta[4:8], 1080)       // width
	binary.BigEndian.PutUint32(sessionMeta[8:12], 2400)      // height
	if _, err := conn.Write(sessionMeta); err != nil {
		return
	}
	// 等测试订阅完成
	<-f.framesReady
	writeFrame(conn, true, false, 0, []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x29}) // SPS/PPS
	writeFrame(conn, false, true, 1000, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB})    // IDR
	writeFrame(conn, false, false, 2000, []byte{0x01, 0x02, 0x03})                           // delta
	time.Sleep(3 * time.Second)                                                              // 保持连接，等待测试读取
}

// writeFrame 按 scrcpy v5 帧头格式写包：bit63=SESSION、bit62=config、bit61=key、低 61 位 PTS
func writeFrame(conn net.Conn, config, key bool, pts uint64, data []byte) {
	var flags uint64
	if config {
		flags |= 1 << 62
	}
	if key {
		flags |= 1 << 61
	}
	flags |= pts
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint64(hdr[0:8], flags)
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(data)))
	_, _ = conn.Write(hdr)
	_, _ = conn.Write(data)
}

func TestStreamHandshakeAndFanout(t *testing.T) {
	deps := &fakeDeps{framesReady: make(chan struct{})}
	s, err := start(deps, "fake-serial", []byte("jar-bytes"), "2.4", Options{})
	if err != nil {
		t.Fatalf("start 失败: %v", err)
	}
	defer func() {
		s.Stop()
		if deps.shellEnd != nil {
			deps.shellEnd.Close()
		}
	}()

	info := s.Info()
	if info.DeviceName != "FakeDevice" {
		t.Errorf("设备名 = %q, want FakeDevice", info.DeviceName)
	}
	if info.Codec != "h264" || info.Width != 1080 || info.Height != 2400 {
		t.Errorf("元数据错误: %+v", info)
	}

	sub, cancel, cfg := s.Subscribe()
	defer cancel()
	if cfg != nil {
		t.Errorf("订阅早于配置包，缓存应为 nil，得到 %d 字节", len(cfg))
	}
	close(deps.framesReady) // 开始发帧

	expect := []struct {
		config, key bool
		pts         uint64
		size        int
	}{
		{true, false, 0, 8},
		{false, true, 1000, 7},
		{false, false, 2000, 3},
	}
	for i, want := range expect {
		select {
		case p := <-sub.Chan():
			if p.Config != want.config || p.Key != want.key || p.PTS != want.pts || len(p.Data) != want.size {
				t.Errorf("帧 %d = {config:%v key:%v pts:%d size:%d}, want %+v",
					i, p.Config, p.Key, p.PTS, len(p.Data), want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("帧 %d 超时未收到", i)
		}
	}
}

func TestAudioStream(t *testing.T) {
	deps := &fakeDeps{
		framesReady: make(chan struct{}),
		withAudio:   true,
		audioReady:  make(chan struct{}),
	}
	audioOn := true
	s, err := start(deps, "fake-serial", []byte("jar"), "2.4", Options{Audio: &audioOn})
	if err != nil {
		t.Fatalf("start 失败: %v", err)
	}
	defer func() {
		s.Stop()
		if deps.shellEnd != nil {
			deps.shellEnd.Close()
		}
	}()

	// 音频 setup 异步：先等 fake 发出 opus 头（不依赖 audioReady）、setup 完成
	deadline := time.Now().Add(3 * time.Second)
	for !s.Info().Audio && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !s.Info().Audio {
		t.Fatal("音频应可用")
	}

	// 先订阅，再放行音频帧（避免帧在订阅前被 hub 丢弃）
	sub, cancel, _, err := s.SubscribeAudio()
	if err != nil {
		t.Fatalf("订阅音频失败: %v", err)
	}
	defer cancel()
	close(deps.audioReady)
	close(deps.framesReady)

	// 配置包经通道到达（订阅早于帧发送，缓存必为 nil）
	select {
	case p := <-sub.Chan():
		if !p.Config || !bytes.HasPrefix(p.Data, []byte("OpusHead")) {
			t.Fatalf("首个音频包应为 OpusHead 配置包: %x", p.Data)
		}
		if ch := p.Data[9]; ch != 2 {
			t.Errorf("channels = %d, want 2", ch)
		}
		if sr := binary.LittleEndian.Uint32(p.Data[12:]); sr != 48000 {
			t.Errorf("sampleRate = %d, want 48000", sr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("音频配置包超时")
	}

	// 音频帧（PTS=1000 的 delta）
	frameDeadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case p := <-sub.Chan():
			if p.Config {
				continue // 跳过重复配置包
			}
			if !p.Key && p.PTS != 1000 {
				continue
			}
			if p.PTS != 1000 || string(p.Data) != "\xFC\xFF\xFE" {
				t.Errorf("音频帧错误: %+v %q", p, p.Data)
			}
			return
		case <-time.After(frameDeadline.Sub(time.Now())):
			t.Fatal("音频帧超时")
		}
	}
}

func TestHubSlowSubscriberKeepsReceiving(t *testing.T) {
	h := NewHub()
	sub, cancel, _ := h.Add()
	defer cancel()

	// 灌满订阅通道（128）后继续广播：应丢最旧保最新，而不是剔除订阅者
	for i := 0; i < 200; i++ {
		h.Broadcast(Packet{PTS: uint64(i), Data: []byte{byte(i)}})
	}

	select {
	case _, ok := <-sub.Chan():
		if !ok {
			t.Fatal("订阅通道被关闭（订阅者被剔除），应仅丢帧保活")
		}
	case <-time.After(time.Second):
		t.Fatal("通道应有积压数据")
	}

	// 排空通道后，最新一帧（PTS=199）必须可达
	var last uint64 = ^uint64(0)
	drained := false
	for !drained {
		select {
		case p, ok := <-sub.Chan():
			if !ok {
				t.Fatal("订阅通道被关闭")
			}
			last = p.PTS
		case <-time.After(200 * time.Millisecond):
			drained = true
		}
	}
	if last != 199 {
		t.Errorf("最新帧 PTS = %d, want 199", last)
	}
}

func TestHubLateSubscriberGetsCachedConfig(t *testing.T) {
	h := NewHub()
	h.Broadcast(Packet{Config: true, Data: []byte("spspps")})

	sub, cancel, cfg := h.Add()
	defer cancel()
	if cfg == nil || string(cfg) != "spspps" {
		t.Fatalf("晚到订阅者应拿到缓存配置包, got %v", cfg)
	}
	h.Broadcast(Packet{Key: true, PTS: 7, Data: []byte("idr")})
	select {
	case p := <-sub.Chan():
		if !p.Key || p.PTS != 7 {
			t.Errorf("收到错误帧: %+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("关键帧超时未收到")
	}

	// 关闭后所有订阅通道应被关闭
	h.Close()
	select {
	case _, ok := <-sub.Chan():
		if ok {
			t.Error("Close 后订阅通道应关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("订阅通道未关闭")
	}
}
