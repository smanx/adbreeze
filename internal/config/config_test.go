package config

import (
	"errors"
	"flag"
	"runtime"
	"testing"
)

func TestParseArgsDefaults(t *testing.T) {
	cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("默认参数解析失败: %v", err)
	}
	if cfg.Addr != ":9080" {
		t.Errorf("Addr = %q，期望 :9080", cfg.Addr)
	}
	if cfg.Password != "admin" {
		t.Errorf("Password = %q，期望 admin", cfg.Password)
	}
	if !cfg.HTTPS || cfg.HTTPSAddr != ":9443" {
		t.Errorf("HTTPS = %v %q，期望 true :9443", cfg.HTTPS, cfg.HTTPSAddr)
	}
	// 无参数启动 = 双击 / Finder 场景：桌面平台默认自动开浏览器，Linux 默认不开
	wantOpen := runtime.GOOS != "linux"
	if cfg.OpenBrowser != wantOpen {
		t.Errorf("OpenBrowser = %v，期望 %v（GOOS=%s）", cfg.OpenBrowser, wantOpen, runtime.GOOS)
	}
}

func TestParseArgsWithArgsDisablesBrowser(t *testing.T) {
	// 带参数启动 = 控制台 / 服务场景，不自动开浏览器（显式指定除外）
	for _, args := range [][]string{
		{"-addr", ":9090"},
		{"-https=false"},
		{"-user", "x", "-password", "y"}, // -user 已废弃：仍应被接受（不报未知参数）
	} {
		cfg, err := ParseArgs(args)
		if err != nil {
			t.Fatalf("解析 %v 失败: %v", args, err)
		}
		if cfg.OpenBrowser {
			t.Errorf("参数 %v 不应自动打开浏览器", args)
		}
	}
}

func TestParseArgsExplicitBrowser(t *testing.T) {
	// 显式 -open-browser：带参数也要开
	cfg, err := ParseArgs([]string{"-addr", ":9090", "-open-browser"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !cfg.OpenBrowser {
		t.Error("显式 -open-browser 应开启")
	}
	// 显式 -open-browser=false：无参数也不开
	cfg, err = ParseArgs([]string{"-open-browser=false"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.OpenBrowser {
		t.Error("显式 -open-browser=false 应关闭")
	}
}

func TestParseArgsEnvBrowser(t *testing.T) {
	// 环境变量显式关闭：带参数也不开
	t.Setenv("ADBREEZE_OPEN_BROWSER", "false")
	cfg, err := ParseArgs([]string{"-addr", ":9090"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.OpenBrowser {
		t.Error("ADBREEZE_OPEN_BROWSER=false 应关闭")
	}
	// 环境变量显式开启：带参数也开（显式指定不受默认语义约束）
	t.Setenv("ADBREEZE_OPEN_BROWSER", "true")
	cfg, err = ParseArgs([]string{"-addr", ":9090"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !cfg.OpenBrowser {
		t.Error("ADBREEZE_OPEN_BROWSER=true 应开启")
	}
}

func TestParseArgsEnvOverrides(t *testing.T) {
	t.Setenv("ADBREEZE_ADDR", "9091")
	t.Setenv("ADBREEZE_PASSWORD", "s3cret")
	cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.Addr != ":9091" {
		t.Errorf("Addr = %q，期望 :9091（纯端口号应归一化）", cfg.Addr)
	}
	if cfg.Password != "s3cret" {
		t.Errorf("Password = %q，期望 s3cret", cfg.Password)
	}
	// 命令行优先于环境变量
	cfg, err = ParseArgs([]string{"-addr", ":9092"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.Addr != ":9092" {
		t.Errorf("Addr = %q，期望 :9092（命令行应覆盖环境变量）", cfg.Addr)
	}
}

func TestParseArgsValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"证书只指定一半", []string{"-tls-cert", "cert.pem"}},
		{"HTTP 与 HTTPS 同端口", []string{"-addr", ":9080", "-https-addr", "9080"}},
		{"未知参数", []string{"-bogus"}},
	}
	for _, c := range cases {
		if _, err := ParseArgs(c.args); err == nil {
			t.Errorf("%s：应返回 error", c.name)
		}
	}
	// -h 返回 flag.ErrHelp（用法信息已由 flag 包打印）
	_, err := ParseArgs([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h 应返回 flag.ErrHelp，得到 %v", err)
	}
}

func TestNormalizeAddr(t *testing.T) {
	cases := map[string]string{
		"9080":           ":9080",
		":9080":          ":9080",
		"0.0.0.0:9080":   "0.0.0.0:9080",
		"[::1]:9080":     "[::1]:9080",
		"localhost:9080": "localhost:9080",
		"":               "",
	}
	for in, want := range cases {
		if got := normalizeAddr(in); got != want {
			t.Errorf("normalizeAddr(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestParseArgsTunnelDefaults(t *testing.T) {
	cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("默认参数解析失败: %v", err)
	}
	if cfg.Tunnel {
		t.Error("Tunnel 默认应为 false（安装包 +0，功能默认关闭）")
	}
	if cfg.TunnelToken != "" {
		t.Errorf("TunnelToken 默认应为空，得到 %q", cfg.TunnelToken)
	}
}

func TestParseArgsTunnelFlags(t *testing.T) {
	cfg, err := ParseArgs([]string{"-tunnel", "-tunnel-token", "eyJhIjoiSXYifQ=="})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !cfg.Tunnel {
		t.Error("-tunnel 应开启")
	}
	if cfg.TunnelToken != "eyJhIjoiSXYifQ==" {
		t.Errorf("TunnelToken = %q，期望传入的 token", cfg.TunnelToken)
	}
}

func TestParseArgsWebRTCSTUN(t *testing.T) {
	// 默认：两个公共 STUN（不配则跨 NAT 建连必失败）
	cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("默认参数解析失败: %v", err)
	}
	if cfg.WebRTCSTUN != "stun.cloudflare.com:3478,stun.l.google.com:19302" {
		t.Errorf("WebRTCSTUN 默认值 = %q", cfg.WebRTCSTUN)
	}
	// 命令行覆盖
	cfg, err = ParseArgs([]string{"-webrtc-stun", "turn:my.coturn:3478"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.WebRTCSTUN != "turn:my.coturn:3478" {
		t.Errorf("WebRTCSTUN = %q，期望命令行值", cfg.WebRTCSTUN)
	}
	// 环境变量覆盖（命令行未指定时）
	t.Setenv("ADBREEZE_WEBRTC_STUN", "off")
	cfg, err = ParseArgs(nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.WebRTCSTUN != "off" {
		t.Errorf("WebRTCSTUN = %q，期望环境变量值 off", cfg.WebRTCSTUN)
	}
	// 命令行优先于环境变量
	cfg, err = ParseArgs([]string{"-webrtc-stun", "stun:a.com:3478"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.WebRTCSTUN != "stun:a.com:3478" {
		t.Errorf("命令行应覆盖环境变量，得到 %q", cfg.WebRTCSTUN)
	}
}

func TestParseArgsTunnelEnv(t *testing.T) {
	t.Setenv("ADBREEZE_TUNNEL", "true")
	t.Setenv("ADBREEZE_TUNNEL_TOKEN", "env-token")
	cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !cfg.Tunnel || cfg.TunnelToken != "env-token" {
		t.Errorf("环境变量未生效: Tunnel=%v Token=%q", cfg.Tunnel, cfg.TunnelToken)
	}
	// 命令行优先于环境变量
	cfg, err = ParseArgs([]string{"-tunnel=false", "-tunnel-token", "flag-token"})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.Tunnel || cfg.TunnelToken != "flag-token" {
		t.Errorf("命令行应覆盖环境变量: Tunnel=%v Token=%q", cfg.Tunnel, cfg.TunnelToken)
	}
	// 非法布尔值：忽略该环境变量，不阻断启动
	t.Setenv("ADBREEZE_TUNNEL", "yes")
	cfg, err = ParseArgs(nil)
	if err != nil {
		t.Fatalf("非法布尔值不应阻断启动: %v", err)
	}
	if cfg.Tunnel {
		t.Error("ADBREEZE_TUNNEL=yes 不是合法布尔值，应被忽略")
	}
}
