package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetTopKeyModel(t *testing.T) {
	src := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"model\": \"old/model\"\n}\n"
	out, err := setTopKey(src, "model", `"new/model"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"model": "new/model"`) {
		t.Fatalf("model not replaced: %s", out)
	}
	if !strings.Contains(out, "$schema") {
		t.Fatalf("other field lost: %s", out)
	}
}

func TestSetTopKeyInsert(t *testing.T) {
	src := "{\n  \"$schema\": \"x\"\n}\n"
	out, err := setTopKey(src, "model", `"new/model"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"model": "new/model"`) {
		t.Fatalf("model not inserted: %s", out)
	}
	if !strings.Contains(out, "$schema") {
		t.Fatalf("schema lost: %s", out)
	}
}

func TestSetNestedMCP(t *testing.T) {
	src := "{\n  \"$schema\": \"x\",\n  \"model\": \"m\"\n}\n"
	entry := `{"type": "local", "command": ["npx", "-y", "@mobilenext/mobile-mcp@latest"]}`
	out, err := setNestedKey(src, []string{"mcp", "servers", "mobile-mcp"}, entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mcp"`, `"servers"`, `"mobile-mcp"`, "@mobilenext/mobile-mcp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
	// 再次设置应覆盖而非重复
	out2, err := setNestedKey(out, []string{"mcp", "servers", "mobile-mcp"}, entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out2, `"mobile-mcp"`) != 1 {
		t.Fatalf("duplicate mobile-mcp key: %s", out2)
	}
}

func TestMCPStyleDetect(t *testing.T) {
	v1 := "{\n  \"mcp\": {\n    \"chrome-devtools\": {\"type\": \"local\"}\n  }\n}"
	if got := mcpStyle(v1); got != "v1" {
		t.Fatalf("expected v1, got %s", got)
	}
	v2 := "{\n  \"mcp\": {\n    \"servers\": {\n      \"mobile-mcp\": {\"type\": \"local\"}\n    }\n  }\n}"
	if got := mcpStyle(v2); got != "v2" {
		t.Fatalf("expected v2, got %s", got)
	}
	none := "{\n  \"model\": \"x\"\n}"
	if got := mcpStyle(none); got != "none" {
		t.Fatalf("expected none, got %s", got)
	}
}

func TestRegisterMobileMCPV1Style(t *testing.T) {
	src := "{\n  \"mcp\": {\n    \"chrome-devtools\": {\"type\": \"local\"}\n  }\n}"
	entry := `{"type": "local", "command": ["npx", "-y", "@mobilenext/mobile-mcp@latest"]}`
	out, err := setNestedKey(src, []string{"mcp", "mobile-mcp"}, entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := findNestedKey(out, []string{"mcp", "mobile-mcp"}); !ok {
		t.Fatalf("mobile-mcp not registered in V1 style: %s", out)
	}
	if !strings.Contains(out, "chrome-devtools") {
		t.Fatalf("existing server lost: %s", out)
	}
}

func TestFindNestedExisting(t *testing.T) {
	src := `{
  "mcp": {
    "servers": {
      "mobile-mcp": {"type": "local"},
      "other": {"type": "remote"}
    }
  }
}`
	s, e, ok := findNestedKey(src, []string{"mcp", "servers", "mobile-mcp"})
	if !ok {
		t.Fatal("not found")
	}
	if !strings.Contains(src[s:e], "local") {
		t.Fatalf("wrong span: %q", src[s:e])
	}
}

// --- 配置文件写入安全（回归：备份必须是旧内容，而不是新内容） ---

// withTempConfig 把 XDG_CONFIG_HOME 指向临时目录，返回 opencode 配置路径。
func withTempConfig(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "opencode", "opencode.json")
	if initial != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func backupsOf(path string) []string {
	entries, _ := os.ReadDir(filepath.Dir(path))
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(path)+".") && strings.HasSuffix(e.Name(), ".bak") {
			out = append(out, filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
	return out
}

func TestWriteConfigBacksUpOldContent(t *testing.T) {
	old := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\"a\": {}}\n}\n"
	path := withTempConfig(t, old)
	newContent := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\"b\": {}}\n}\n"

	if err := WriteConfig(newContent); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != newContent {
		t.Fatalf("file not updated: %s", got)
	}
	baks := backupsOf(path)
	if len(baks) != 1 {
		t.Fatalf("expected exactly 1 backup, got %v", baks)
	}
	bak, _ := os.ReadFile(baks[0])
	if string(bak) != old {
		t.Fatalf("backup must contain OLD content, got: %s", bak)
	}
}

func TestWriteConfigRejectsInvalidAndKeepsFile(t *testing.T) {
	old := "{\n  \"provider\": {}\n}\n"
	path := withTempConfig(t, old)

	if err := WriteConfig("{ broken "); err == nil {
		t.Fatal("expected error for invalid JSONC")
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Fatalf("file must be untouched, got: %s", got)
	}
	if baks := backupsOf(path); len(baks) != 0 {
		t.Fatalf("no backup expected on rejected write, got %v", baks)
	}
}

func TestWriteConfigDoesNotOverwriteBackups(t *testing.T) {
	path := withTempConfig(t, "{\n  \"v\": 1\n}\n")
	if err := WriteConfig("{\n  \"v\": 2\n}\n"); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig("{\n  \"v\": 3\n}\n"); err != nil {
		t.Fatal(err)
	}
	baks := backupsOf(path)
	if len(baks) != 2 {
		t.Fatalf("expected 2 distinct backups, got %v", baks)
	}
}

func TestWriteModelPreservesAndBacksUp(t *testing.T) {
	old := "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"provider\": {\"p\": {\"models\": {\"m\": {}}}}\n}\n"
	path := withTempConfig(t, old)

	if err := WriteModel("p/m"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"model": "p/m"`) {
		t.Fatalf("model not written: %s", got)
	}
	if !strings.Contains(string(got), `"provider"`) {
		t.Fatalf("provider lost: %s", got)
	}
	if ReadModel() != "p/m" {
		t.Fatalf("ReadModel = %q", ReadModel())
	}
	baks := backupsOf(path)
	if len(baks) != 1 {
		t.Fatalf("expected 1 backup, got %v", baks)
	}
	bak, _ := os.ReadFile(baks[0])
	if string(bak) != old {
		t.Fatalf("backup must contain OLD content")
	}
}
