package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain 兼作 cloudflared 替身：被测进程（os.Args[0]）被 Manager 以
// TUNNEL_TOKEN=<marker> 拉起时，不跑测试，而是模仿 cloudflared 的行为——
// 向 stderr 打印"隧道已创建"横幅（真实 cloudflared 就把横幅打在 stderr），
// 然后按 marker 决定挂起还是立即退出。这样生命周期测试无需真实二进制。
func TestMain(m *testing.M) {
	switch os.Getenv("TUNNEL_TOKEN") {
	case "stub-sleep":
		fmt.Fprintln(os.Stderr, "INF +--------------------------------------------------------------------------------------------+")
		fmt.Fprintln(os.Stderr, "INF |  Your quick Tunnel has been created! Visit it at (it may take a few seconds to establish):  |")
		fmt.Fprintln(os.Stderr, "INF |  https://stub-tunnel-1234.trycloudflare.com                                                |")
		fmt.Fprintln(os.Stderr, "INF +--------------------------------------------------------------------------------------------+")
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	case "stub-exit":
		fmt.Fprintln(os.Stderr, "INF |  https://stub-tunnel-1234.trycloudflare.com  |")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestBuildArgs(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "quick 默认指向 127.0.0.1",
			opts: Options{HTTPPort: "9080"},
			want: []string{"tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:9080"},
		},
		{
			name: "quick 自定义 service",
			opts: Options{Service: "http://127.0.0.1:9443", HTTPPort: "9080"},
			want: []string{"tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:9443"},
		},
		{
			name: "quick 指定协议",
			opts: Options{HTTPPort: "9080", Protocol: "http2"},
			want: []string{"tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:9080", "--protocol", "http2"},
		},
		{
			name: "named 不带 --url（入口由 token 决定）",
			opts: Options{Token: "secret-token", Service: "http://127.0.0.1:9443", HTTPPort: "9080"},
			want: []string{"tunnel", "--no-autoupdate", "run"},
		},
		{
			name: "named 指定协议",
			opts: Options{Token: "secret-token", Protocol: "http2"},
			want: []string{"tunnel", "--no-autoupdate", "run", "--protocol", "http2"},
		},
	}
	for _, c := range cases {
		got := buildArgs(c.opts)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: buildArgs = %v，期望 %v", c.name, got, c.want)
		}
		// 安全红线：token 绝不进 argv（ps 可见）
		for _, a := range got {
			if c.opts.Token != "" && strings.Contains(a, c.opts.Token) {
				t.Errorf("%s: token 泄漏进命令行参数: %v", c.name, got)
			}
		}
	}
}

func TestServiceURLDefault(t *testing.T) {
	if got := (Options{HTTPPort: "9080"}).serviceURL(); got != "http://127.0.0.1:9080" {
		t.Errorf("serviceURL = %q，期望 http://127.0.0.1:9080", got)
	}
	if got := (Options{Service: "http://127.0.0.1:9443", HTTPPort: "9080"}).serviceURL(); got != "http://127.0.0.1:9443" {
		t.Errorf("serviceURL = %q，期望自定义值", got)
	}
}

func TestMode(t *testing.T) {
	if got := (Options{}).Mode(); got != "quick" {
		t.Errorf("Mode = %q，期望 quick", got)
	}
	if got := (Options{Token: "x"}).Mode(); got != "named" {
		t.Errorf("Mode = %q，期望 named", got)
	}
}

func TestMaskToken(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"short":                        "****",
		"12345678":                     "****",
		"eyJhIjoiSXYifQeyJhIjoiSXYifQ": "eyJh****YifQ",
	}
	for in, want := range cases {
		if got := MaskToken(in); got != want {
			t.Errorf("MaskToken(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestURLRegex(t *testing.T) {
	lines := []string{
		"INF +--------------------------------------------------------------------------------------------+",
		"INF |  Your quick Tunnel has been created! Visit it at (it may take a few seconds to establish):  |",
		"INF |  https://random-words-12ab.trycloudflare.com                                                |",
		"INF +--------------------------------------------------------------------------------------------+",
		"ERR Failed to dial: connection refused", // 无关行不应误命中
	}
	want := "https://random-words-12ab.trycloudflare.com"
	for _, line := range lines {
		got := urlRe.FindString(line)
		if line == lines[2] {
			if got != want {
				t.Errorf("URL 行解析得到 %q，期望 %q", got, want)
			}
		} else if got != "" {
			t.Errorf("无关行 %q 误命中 URL: %q", line, got)
		}
	}
}

func TestScanLines(t *testing.T) {
	m := &Manager{}
	urlCh := make(chan string, 1)
	in := "INF starting\nINF |  https://abc-123-def.trycloudflare.com  |\nINF registered tunnel connection\n"
	go scanLines(bytes.NewReader([]byte(in)), m, urlCh)

	select {
	case u := <-urlCh:
		if u != "https://abc-123-def.trycloudflare.com" {
			t.Errorf("解析到 %q", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scanLines 未解析出 URL")
	}
	// 等日志写完
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := m.Status()
		if len(st.Logs) >= 3 {
			if !strings.Contains(st.Logs[1], "trycloudflare.com") {
				t.Errorf("日志未含 URL 行: %v", st.Logs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("日志行数不足: %v", st.Logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestResolveBinaryExplicit(t *testing.T) {
	dir := t.TempDir()
	// 直接指向可执行文件
	bin := filepath.Join(dir, binName())
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary(Options{Binary: bin})
	if err != nil || got != bin {
		t.Errorf("ResolveBinary(文件) = %q, %v；期望 %q", got, err, bin)
	}
	// 指向目录：自动拼接可执行名
	got, err = ResolveBinary(Options{Binary: dir})
	if err != nil || got != bin {
		t.Errorf("ResolveBinary(目录) = %q, %v；期望 %q", got, err, bin)
	}
	// 不存在的路径：报错说清原因
	if _, err := ResolveBinary(Options{Binary: filepath.Join(dir, "nope")}); err == nil {
		t.Error("不存在的路径应报错")
	}
	// 空目录（无可执行文件）也应报错
	empty := t.TempDir()
	if _, err := ResolveBinary(Options{Binary: empty}); err == nil {
		t.Error("空目录应报错")
	}
}

func TestResolveBinaryDataDir(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, binName())
	if err := os.WriteFile(bin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary(Options{DataDir: dir})
	if err != nil || got != bin {
		t.Errorf("ResolveBinary(DataDir/bin) = %q, %v；期望 %q", got, err, bin)
	}
}

func TestResolveBinaryNotFound(t *testing.T) {
	// 用一个几乎不可能存在的路径名做 DataDir/Binary，且不改 PATH——
	// 本机可能真装了 cloudflared，故只断言错误信息含安装指引
	dir := t.TempDir()
	_, err := ResolveBinary(Options{DataDir: dir})
	if err == nil {
		t.Skip("本机 PATH 里存在 cloudflared，跳过未安装场景")
	}
	if !strings.Contains(err.Error(), "安装") {
		t.Errorf("错误信息应含安装指引: %v", err)
	}
}

// waitState 轮询等待 Manager 进入目标状态。
func waitState(t *testing.T, m *Manager, want string, within time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		st := m.Status()
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待状态 %q 超时，当前: %+v", want, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{Binary: os.Args[0], Token: "stub-sleep", AutoRestart: true, HTTPPort: "9080"})

	if err := m.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	// Start 幂等：重复调用不应拉起第二个子进程
	if err := m.Start(); err != nil {
		t.Fatalf("重复 Start 失败: %v", err)
	}

	st := waitState(t, m, stateRunning, 15*time.Second)
	if st.URL != "https://stub-tunnel-1234.trycloudflare.com" {
		t.Errorf("URL = %q，期望替身横幅里的地址", st.URL)
	}
	if st.Mode != "named" {
		t.Errorf("Mode = %q，期望 named", st.Mode)
	}
	if st.BinaryPath != os.Args[0] {
		t.Errorf("BinaryPath = %q，期望 %q", st.BinaryPath, os.Args[0])
	}
	// 日志里应有启动注解与横幅，且不含 token
	joined := strings.Join(st.Logs, "\n")
	if !strings.Contains(joined, "stub-tunnel-1234") {
		t.Errorf("日志缺少横幅内容: %v", st.Logs)
	}
	if strings.Contains(joined, "stub-sleep") {
		t.Errorf("日志泄漏了 token: %v", st.Logs)
	}

	m.Stop()
	m.Stop() // 幂等
	if st := m.Status(); st.State != stateStopped {
		t.Errorf("Stop 后 State = %q，期望 stopped", st.State)
	}
}

func TestManagerAutoRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 替身打印横幅后立即以 1 退出：Manager 应自动重启（退避 1s→2s→4s…）
	m := New(ctx, Options{Binary: os.Args[0], Token: "stub-exit", AutoRestart: true, HTTPPort: "9080"})
	if err := m.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if m.Status().Restarts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("自动重启未发生，当前: %+v", m.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.Stop()
	if st := m.Status(); st.State != stateStopped {
		t.Errorf("Stop 后 State = %q，期望 stopped", st.State)
	}
}

func TestManagerNoAutoRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{Binary: os.Args[0], Token: "stub-exit", AutoRestart: false, HTTPPort: "9080"})
	if err := m.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	// 关闭自动重启：失败一次后停在 failed，不应重启
	waitState(t, m, stateFailed, 15*time.Second)
	time.Sleep(2 * time.Second) // 超过首次退避，确认没有第二次尝试
	if st := m.Status(); st.Restarts != 0 {
		t.Errorf("AutoRestart=false 时不应重启，Restarts = %d", st.Restarts)
	}
	m.Stop()
}

func TestManagerCtxCancelStopsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, Options{Binary: os.Args[0], Token: "stub-sleep", AutoRestart: true, HTTPPort: "9080"})
	if err := m.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	waitState(t, m, stateRunning, 15*time.Second)
	cancel() // 不调 Stop，直接取消 ctx：supervisor 应自行收敛
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := m.Status()
		if st.State == stateStopped || st.State == stateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ctx 取消后状态未收敛: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestManagerBinaryMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(ctx, Options{Binary: filepath.Join(t.TempDir(), "nonexistent"), AutoRestart: false, HTTPPort: "9080"})
	if err := m.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	st := waitState(t, m, stateFailed, 5*time.Second)
	if st.Error == "" {
		t.Error("二进制缺失时应记录 Error")
	}
	m.Stop()
}

func TestProbeMissingBinary(t *testing.T) {
	res := Probe(Options{Binary: filepath.Join(t.TempDir(), "nonexistent")})
	if res.Path != "" || res.Error == "" {
		t.Errorf("Probe 应报告失败: %+v", res)
	}
}
