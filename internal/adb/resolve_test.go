package adb

import (
	"os"
	"path/filepath"
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

func TestCommonAdbDirs(t *testing.T) {
	home := isolateEnv(t)
	t.Setenv("ANDROID_HOME", `C:\sdk`)

	dirs := commonAdbDirs()
	want := map[string]bool{
		filepath.Join(`C:\sdk`, "platform-tools"): false,
	}
	if home != "" {
		// 至少应包含与 home 相关的平台标准目录之一
		want[filepath.Join(home, "platform-tools")] = false
	}
	for d := range want {
		found := false
		for _, got := range dirs {
			if got == d {
				found = true
				break
			}
		}
		if !found {
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
