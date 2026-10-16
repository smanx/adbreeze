package main

import (
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestHostPort(t *testing.T) {
	cases := map[string]string{
		":9080":          "localhost:9080",
		"0.0.0.0:9080":   "localhost:9080",
		"[::]:9443":      "localhost:9443",
		"127.0.0.1:80":   "127.0.0.1:80",
		"192.168.1.5:9":  "192.168.1.5:9",
		"localhost:9080": "localhost:9080",
	}
	for in, want := range cases {
		if got := hostPort(in); got != want {
			t.Errorf("hostPort(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestWaitPort(t *testing.T) {
	// 起一个真实监听：应立即就绪
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !waitPort(ln.Addr().String(), 2*time.Second) {
		t.Error("已监听的端口应立即可连")
	}
	// 没人监听的端口：超时返回 false
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln2.Addr().String()
	ln2.Close()
	if waitPort(dead, 400*time.Millisecond) {
		t.Error("无人监听的端口应超时失败")
	}
}

func TestWaitBeforeExit(t *testing.T) {
	// 测试二进制的 os.Args 形如 [/tmp/go-build.../test -test.timeout=10m]，
	// 必然「带参数」；只有 Darwin 且 TERM_PROGRAM 为空时才可能为 true。
	got := waitBeforeExit()
	want := runtime.GOOS == "darwin" && os.Getenv("TERM_PROGRAM") == "" && len(os.Args) <= 1
	if got != want {
		t.Errorf("waitBeforeExit() = %v，期望 %v（GOOS=%s, args=%v）", got, want, runtime.GOOS, os.Args)
	}
}
