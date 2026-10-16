package adb

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateEnv 把可能指向真实 SDK 的环境变量与用户目录全部指向临时目录，
// 保证用例只受自己伪造的数据影响（CI 上尤其重要）。
func isolateEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	return dir
}

// fakeAdb 在指定目录下造一个可执行的假 adb，返回其路径。
func fakeAdb(t *testing.T, dir string) string {
	t.Helper()
	pt := filepath.Join(dir, "platform-tools")
	if err := os.MkdirAll(pt, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(pt, adbBinaryName())
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveAdbPathFromPATH(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, adbBinaryName())
	if err := os.WriteFile(want, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")

	got, err := ResolveAdbPath("")
	if err != nil {
		t.Fatalf("PATH 里的 adb 应被找到: %v", err)
	}
	if got != want {
		t.Errorf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveAdbPathDiscoversCommonDir(t *testing.T) {
	// PATH 为空、常见位置（这里用 ANDROID_HOME）有 adb：应补漏成功
	home := isolateEnv(t)
	want := fakeAdb(t, home)
	t.Setenv("ANDROID_HOME", home)

	got, err := ResolveAdbPath("")
	if err != nil {
		t.Fatalf("常见位置里的 adb 应被找到: %v", err)
	}
	if got != want {
		t.Errorf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveAdbPathNotFound(t *testing.T) {
	// PATH 为空、常见位置全空：报错且信息说明找过哪些地方
	isolateEnv(t)
	if _, err := ResolveAdbPath(""); err == nil {
		t.Fatal("处处找不到 adb 时应报错")
	}
}

func TestResolveAdbPathDirNormalization(t *testing.T) {
	// 用户填目录：拼上平台对应文件名
	dir := t.TempDir()
	want := filepath.Join(dir, adbBinaryName())
	if err := os.WriteFile(want, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAdbPath(dir)
	if err != nil {
		t.Fatalf("目录应被归一化: %v", err)
	}
	if got != want {
		t.Errorf("解析结果 = %q，期望 %q", got, want)
	}
}

func TestResolveAdbPathExplicitMissing(t *testing.T) {
	if _, err := ResolveAdbPath(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("显式指定的路径不存在时应报错")
	}
}

func TestHomeAdbDirs(t *testing.T) {
	// 纯函数：三个平台的标准 SDK 目录都能在本机验证（CI 在 Linux 上跑，
	// Windows/macOS 分支以前只能靠 CI 报错才发现问题）。
	cases := []struct {
		goos string
		want []string
	}{
		{"windows", []string{"AppData/Local/Android/Sdk/platform-tools", "platform-tools"}},
		{"darwin", []string{"Library/Android/sdk/platform-tools"}},
		{"linux", []string{"Android/Sdk/platform-tools"}},
	}
	for _, c := range cases {
		got := homeAdbDirs(c.goos, "/home/tester")
		if len(got) != len(c.want) {
			t.Errorf("%s: 得到 %v，期望 %v", c.goos, got, c.want)
			continue
		}
		for i, w := range c.want {
			if g := filepath.ToSlash(got[i]); g != "/home/tester/"+w {
				t.Errorf("%s: 第 %d 项 = %q，期望 /home/tester/%s", c.goos, i, g, w)
			}
		}
	}
}

func TestCommonAdbDirs(t *testing.T) {
	home := isolateEnv(t)
	t.Setenv("ANDROID_HOME", `C:\sdk`)

	dirs := commonAdbDirs()
	contains := func(d string) bool {
		for _, got := range dirs {
			if got == d {
				return true
			}
		}
		return false
	}
	// ANDROID_HOME 派生的位置各平台一致
	if !contains(filepath.Join(`C:\sdk`, "platform-tools")) {
		t.Errorf("常见位置里应包含 ANDROID_HOME 派生目录，实际: %v", dirs)
	}
	// 当前平台的 home 标准目录必须都在列（与纯函数交叉验证）
	for _, d := range homeAdbDirs(runtime.GOOS, home) {
		if !contains(d) {
			t.Errorf("常见位置里应包含 %q，实际: %v", d, dirs)
		}
	}
	// 去重
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			t.Errorf("常见位置出现重复项: %q", d)
		}
		seen[d] = true
	}
}
