package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 旧版本存档（只有 adbPath）必须能解析：Tunnel 整体缺省按零值处理，
// AutoRestart 的指针语义保证"未设置 = 开启"而不是 false。
func TestTunnelBackwardCompat(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"adbPath":"/usr/bin/adb"}`), &s); err != nil {
		t.Fatalf("旧存档解析失败: %v", err)
	}
	if s.AdbPath != "/usr/bin/adb" {
		t.Errorf("AdbPath = %q", s.AdbPath)
	}
	if s.Tunnel.Binary != "" || s.Tunnel.Token != "" || s.Tunnel.Service != "" {
		t.Errorf("旧存档的 Tunnel 应为零值: %+v", s.Tunnel)
	}
	if s.Tunnel.ResumeEnabled() {
		t.Error("旧存档缺 resume 字段应按不恢复处理")
	}
	if !s.Tunnel.AutoRestartEnabled() {
		t.Error("旧存档缺 autoRestart 字段应按开启处理")
	}
}

func TestTunnelAutoRestartPointerSemantics(t *testing.T) {
	// 显式 false：存档里必须是 false，且 Get 后仍可区分
	no := false
	var s Settings
	if err := json.Unmarshal([]byte(`{"tunnel":{"autoRestart":false}}`), &s); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s.Tunnel.AutoRestartEnabled() {
		t.Error("autoRestart=false 应关闭自动重启")
	}
	// 序列化 round-trip：指针不被错误地固化成 true
	s.Tunnel.AutoRestart = &no
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), `"autoRestart":false`) {
		t.Errorf("序列化丢掉了显式 false: %s", data)
	}
}

func TestInMemoryStore(t *testing.T) {
	s := NewInMemory()
	no := false
	if err := s.Set(Settings{AdbPath: "adb", Tunnel: TunnelConfig{Binary: "cloudflared", AutoRestart: &no}}); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got := s.Get()
	if got.AdbPath != "adb" || got.Tunnel.Binary != "cloudflared" {
		t.Errorf("内存态读写不一致: %+v", got)
	}
	if got.Tunnel.AutoRestartEnabled() {
		t.Error("显式 false 应生效")
	}
	if s.FilePath() != "" {
		t.Errorf("内存态 FilePath 应为空，得到 %q", s.FilePath())
	}
}

func TestResumeEnabled(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		cfg  TunnelConfig
		want bool
	}{
		{"零值=不恢复", TunnelConfig{}, false},
		{"resume=true", TunnelConfig{Resume: &yes}, true},
		{"resume=false", TunnelConfig{Resume: &no}, false},
		{"旧 autoStart=true 迁移", TunnelConfig{LegacyAutoStart: &yes}, true},
		{"旧 autoStart=false 迁移", TunnelConfig{LegacyAutoStart: &no}, false},
		{"resume 优先于旧字段", TunnelConfig{Resume: &no, LegacyAutoStart: &yes}, false},
	}
	for _, c := range cases {
		if got := c.cfg.ResumeEnabled(); got != c.want {
			t.Errorf("%s: ResumeEnabled() = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// TestLegacyAutoStartMigration 覆盖一次性迁移：旧 autoStart 存档经 Open 后
// resume 生效，且保存后 autoStart 键消失（固化迁移）。
func TestLegacyAutoStartMigration(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离：不碰真实用户配置目录
	dir := filepath.Join(os.Getenv("APPDATA"), "ADBreeze")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"adbPath":"/usr/bin/adb","tunnel":{"token":"abc","autoStart":true}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := Open()
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if !st.Get().Tunnel.ResumeEnabled() {
		t.Error("旧 autoStart=true 应迁移为 resume=true")
	}
	if st.Get().Tunnel.Resume == nil {
		t.Error("迁移后 Resume 应已实例化（保存时才会写出 resume 键）")
	}

	// 保存后：autoStart 键消失、resume 键存在
	if err := st.Set(st.Get()); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), `"autoStart"`) {
		t.Errorf("迁移后存档仍含旧键: %s", data)
	}
	if !contains(string(data), `"resume"`) {
		t.Errorf("存档缺少 resume 键: %s", data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
