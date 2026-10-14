package adb

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

// fakeAdbServer 模拟 adb server 的 smart socket + 设备端 sync 服务，
// 校验 Push/List/Pull 的完整协议帧。
type fakeAdbServer struct {
	t           *testing.T
	files       []byte // Push 收到的数据 / Pull 返回的数据
	gotSerial   string
	gotSpec     string
	gotData     []byte
	gotListPath string
	gotRecvPath string
}

func TestPushSyncProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &fakeAdbServer{t: t, files: []byte("JARJARJAR")}
	go func() { _ = srv.serve(ln) }()

	c := NewClient(ln.Addr().String())
	if err := c.Push("fake-serial", "/data/local/tmp/scrcpy-server.jar", []byte("JARJARJAR"), "0644"); err != nil {
		t.Fatalf("Push 失败: %v", err)
	}

	if srv.gotSerial != "fake-serial" {
		t.Errorf("transport serial = %q", srv.gotSerial)
	}
	wantSpec := "/data/local/tmp/scrcpy-server.jar,0644"
	if srv.gotSpec != wantSpec {
		t.Errorf("SEND spec = %q, want %q", srv.gotSpec, wantSpec)
	}
	if string(srv.gotData) != "JARJARJAR" {
		t.Errorf("DATA = %q, want JARJARJAR", srv.gotData)
	}
}

func TestListSyncProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &fakeAdbServer{t: t}
	go func() { _ = srv.serve(ln) }()

	c := NewClient(ln.Addr().String())
	entries, err := c.List("fake-serial", "/sdcard")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if srv.gotListPath != "/sdcard" {
		t.Errorf("LIST path = %q", srv.gotListPath)
	}
	if len(entries) != 2 || entries[0].Name != "Android" || !entries[0].Dir || entries[1].Name != "a.txt" || entries[1].Size != 9 {
		t.Errorf("entries = %+v", entries)
	}
}

func TestPullSyncProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &fakeAdbServer{t: t, files: []byte("PULL-PAYLOAD")}
	go func() { _ = srv.serve(ln) }()

	c := NewClient(ln.Addr().String())
	var out bytes.Buffer
	if err := c.Pull("fake-serial", "/data/local/tmp/x.bin", &out); err != nil {
		t.Fatalf("Pull 失败: %v", err)
	}
	if srv.gotRecvPath != "/data/local/tmp/x.bin" {
		t.Errorf("RECV path = %q", srv.gotRecvPath)
	}
	if out.String() != "PULL-PAYLOAD" {
		t.Errorf("pull content = %q", out.String())
	}
}

func (s *fakeAdbServer) serve(ln net.Listener) error {
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second)) // 防止协议错位时测试死锁

	// host:transport:<serial>
	cmd, err := readCmd(conn)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(cmd, "host:transport:") {
		return fmt.Errorf("expected host:transport, got %q", cmd)
	}
	s.gotSerial = strings.TrimPrefix(cmd, "host:transport:")
	if _, err := conn.Write([]byte("OKAY")); err != nil {
		return err
	}

	// sync:
	if cmd, err = readCmd(conn); err != nil {
		return err
	}
	if cmd != "sync:" {
		return fmt.Errorf("expected sync:, got %q", cmd)
	}
	if _, err := conn.Write([]byte("OKAY")); err != nil {
		return err
	}

	// sync 通道：SEND / LIST / RECV
	id, payload, err := readSyncRequest(conn)
	if err != nil {
		return err
	}
	switch id {
	case "SEND":
		s.gotSpec = string(payload)
		for {
			var sid [4]byte
			if _, err := io.ReadFull(conn, sid[:]); err != nil {
				return err
			}
			switch string(sid[:]) {
			case "DATA":
				payload, err = readSyncPayload(conn)
				if err != nil {
					return err
				}
				s.gotData = append(s.gotData, payload...)
			case "DONE":
				// 主机侧 DONE = id + u32 mtime；设备回终态
				if _, err := conn.Write([]byte("OKAY")); err != nil {
					return err
				}
				return nil
			default:
				return fmt.Errorf("unexpected sync id %q", sid)
			}
		}
	case "LIST":
		s.gotListPath = string(payload)
		dent := func(name string, mode, size uint32) {
			buf := make([]byte, 16)
			binary.LittleEndian.PutUint32(buf[0:], mode)
			binary.LittleEndian.PutUint32(buf[4:], size)
			binary.LittleEndian.PutUint32(buf[12:], uint32(len(name)))
			conn.Write(append(append([]byte("DENT"), buf...), name...))
		}
		dent("Android", 0x41f8, 0) // 目录
		dent("a.txt", 0x81ff, 9)   // 文件
		conn.Write([]byte("DONE"))
		binary.Write(conn, binary.LittleEndian, uint32(0)) // DONE 自带零长度
		return nil
	case "RECV":
		s.gotRecvPath = string(payload)
		lenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(lenBuf, uint32(len(s.files)))
		conn.Write(append([]byte("DATA"), lenBuf...))
		conn.Write(s.files)
		conn.Write([]byte("DONE"))
		binary.Write(conn, binary.LittleEndian, uint32(0))
		return nil
	default:
		return fmt.Errorf("unexpected sync id %q", id)
	}
}

// readCmd 读取 smart socket 请求：4 字节十六进制长度 + 命令串。
func readCmd(conn net.Conn) (string, error) {
	var hex [4]byte
	if _, err := io.ReadFull(conn, hex[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(hex[:]), 16, 32)
	if err != nil {
		return "", fmt.Errorf("bad hex length %q: %w", hex, err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readSyncRequest 读取 sync 请求：4 字节 id + u32 小端长度 + 载荷。
func readSyncRequest(conn net.Conn) (string, []byte, error) {
	var id [4]byte
	if _, err := io.ReadFull(conn, id[:]); err != nil {
		return "", nil, err
	}
	payload, err := readSyncPayload(conn)
	if err != nil {
		return "", nil, err
	}
	return string(id[:]), payload, nil
}

// readSyncPayload 读取 u32 小端长度 + 载荷。
func readSyncPayload(conn net.Conn) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
