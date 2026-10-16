package browser

import "testing"

func TestCommandFor(t *testing.T) {
	cases := []struct {
		goos    string
		wantCmd string
	}{
		{"windows", "rundll32"},
		{"darwin", "open"},
		{"linux", "xdg-open"},
	}
	for _, c := range cases {
		name, args, err := commandFor(c.goos, "http://localhost:9080")
		if err != nil {
			t.Errorf("%s: 不应报错: %v", c.goos, err)
			continue
		}
		if name != c.wantCmd {
			t.Errorf("%s: 命令 = %q，期望 %q", c.goos, name, c.wantCmd)
		}
		if len(args) == 0 || args[len(args)-1] != "http://localhost:9080" {
			t.Errorf("%s: 参数 = %v，末位应为 URL", c.goos, args)
		}
	}
	// 未知平台报错而非乱执行
	if _, _, err := commandFor("plan9", "http://x"); err == nil {
		t.Error("未知平台应返回错误")
	}
}
