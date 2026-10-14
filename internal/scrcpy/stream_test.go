package scrcpy

import (
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
	ln          net.Listener
	shellEnd    net.Conn
}

func (f *fakeDeps) Push(serial, remote string, data []byte, mode string) error { return nil }

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
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	// control 连接（server 依次连出：video → control），保持打开即可
	go func() {
		ctl, err := f.ln.Accept()
		if err == nil {
			go io.Copy(io.Discard, ctl)
		}
	}()
	f.serveVideo(conn)
}

func (f *fakeDeps) serveVideo(conn net.Conn) {
	defer conn.Close()
	// 握手：dummy + 64B 设备名 + 12B 视频元数据
	if _, err := conn.Write([]byte{0}); err != nil {
		return
	}
	name := make([]byte, 64)
	copy(name, "FakeDevice")
	if _, err := conn.Write(name); err != nil {
		return
	}
	meta := make([]byte, 12)
	binary.BigEndian.PutUint32(meta[0:4], 0x68323634) // "h264"
	binary.BigEndian.PutUint32(meta[4:8], 1080)
	binary.BigEndian.PutUint32(meta[8:12], 2400)
	if _, err := conn.Write(meta); err != nil {
		return
	}
	// 等测试订阅完成
	<-f.framesReady
	writeFrame(conn, true, false, 0, []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x29}) // SPS/PPS
	writeFrame(conn, false, true, 1000, []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA, 0xBB})    // IDR
	writeFrame(conn, false, false, 2000, []byte{0x01, 0x02, 0x03})                           // delta
	time.Sleep(3 * time.Second)                                                              // 保持连接，等待测试读取
}

func writeFrame(conn net.Conn, config, key bool, pts uint64, data []byte) {
	var flags uint64
	if config {
		flags |= 1 << 63
	}
	if key {
		flags |= 1 << 62
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
