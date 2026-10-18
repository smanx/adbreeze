package adb

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdbAssetFor(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantOK       bool
	}{
		{"windows", "amd64", "platform-tools-latest-windows.zip", true},
		{"windows", "arm64", "", false}, // Google 未提供
		{"linux", "amd64", "platform-tools-latest-linux.zip", true},
		{"linux", "arm64", "", false}, // Google 未提供
		{"darwin", "arm64", "platform-tools-latest-darwin.zip", true},
		{"darwin", "amd64", "platform-tools-latest-darwin.zip", true},
		{"android", "arm64", "", false}, // APK 内置 adb，无需安装
		{"plan9", "amd64", "", false},
	}
	for _, c := range cases {
		name, ok := adbAssetFor(c.goos, c.goarch)
		if name != c.want || ok != c.wantOK {
			t.Errorf("adbAssetFor(%s/%s) = %q,%v；期望 %q,%v", c.goos, c.goarch, name, ok, c.want, c.wantOK)
		}
	}
}

// buildPlatformToolsZip 构造形如官方 platform-tools 的 zip（顶层目录
// platform-tools/，内含 adb 及附属文件），供解包测试。
func buildPlatformToolsZip(t *testing.T, adbName string) []byte {
	t.Helper()
	members := map[string]string{
		"platform-tools/" + adbName:        "FAKE-ADB-BINARY-0123456789",
		"platform-tools/NOTICE.txt":        "decoy-doc",
		"platform-tools/source.properties": "decoy-props",
	}
	if runtime.GOOS == "windows" {
		members["platform-tools/AdbWinApi.dll"] = "decoy-dll"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractPlatformTools(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pt.zip")
	adbName := "adb"
	if runtime.GOOS == "windows" {
		adbName = "adb.exe"
	}
	if err := os.WriteFile(zipPath, buildPlatformToolsZip(t, adbName), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "platform-tools")
	adbPath, err := extractPlatformTools(zipPath, dst)
	if err != nil {
		t.Fatalf("extractPlatformTools 失败: %v", err)
	}
	if filepath.Base(adbPath) != adbName {
		t.Errorf("返回路径 = %q，期望基名 %s", adbPath, adbName)
	}
	// adb 内容一致；附属文件也在同目录
	got, err := os.ReadFile(adbPath)
	if err != nil || string(got) != "FAKE-ADB-BINARY-0123456789" {
		t.Errorf("adb 内容 = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "NOTICE.txt")); err != nil {
		t.Errorf("附属文件未解出: %v", err)
	}
	// 包内没有 adb 时报错
	bad := filepath.Join(dir, "bad.zip")
	if err := os.WriteFile(bad, buildPlatformToolsZip(t, "nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractPlatformTools(bad, dst); err == nil {
		t.Error("包内无 adb 时应报错")
	}
}

// newTestAdbInstaller 构造指向测试服务器的安装器：
// 注入无代理的 HTTP client（绕开环境代理变量）与假 smoke（不真实执行）。
func newTestAdbInstaller(t *testing.T, srv *httptest.Server) *AdbInstaller {
	t.Helper()
	in := NewAdbInstaller(t.TempDir())
	in.baseURL = srv.URL + "/"
	in.client = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	in.smoke = func(path string) (string, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(string(b), "FAKE-ADB-BINARY") {
			return "", fmt.Errorf("内容不对: %q", b)
		}
		return "Android Debug Bridge version stub-1.0", nil
	}
	return in
}

func TestAdbInstallerFullFlow(t *testing.T) {
	asset, ok := adbAssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skipf("当前平台 %s/%s 无官方资产，跳过", runtime.GOOS, runtime.GOARCH)
	}
	payload := buildPlatformToolsZip(t, adbBinaryName())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+asset {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	in := newTestAdbInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := in.Status()
		if st.State == AdbInstallDone || st.State == AdbInstallFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("安装未完成: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := in.Status()
	if st.State != AdbInstallDone {
		t.Fatalf("State = %q，期望 done（error=%q）", st.State, st.Error)
	}
	if st.Version != "Android Debug Bridge version stub-1.0" {
		t.Errorf("Version = %q", st.Version)
	}
	if st.Progress != 100 || st.Downloaded != int64(len(payload)) {
		t.Errorf("进度信息 = %d%%/%d，期望 100%%/%d", st.Progress, st.Downloaded, len(payload))
	}
	want := sha256.Sum256(payload)
	if st.SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("SHA256 = %q，期望 %q", st.SHA256, hex.EncodeToString(want[:]))
	}
	// 落点 = <dataDir>/platform-tools/adb(.exe)，内容一致、无临时文件残留
	final := filepath.Join(in.dataDir, "platform-tools", adbBinaryName())
	got, err := os.ReadFile(final)
	if err != nil || string(got) != "FAKE-ADB-BINARY-0123456789" {
		t.Errorf("安装文件内容异常: %q, %v", got, err)
	}
	if _, err := os.Stat(final + ".zip.download"); !os.IsNotExist(err) {
		t.Error("临时文件未清理")
	}
	// 安装后 ResolveAdbPath("") 能直接发现它（discoverAdb 会探可执行文件
	// 同目录的 platform-tools；这里直接验证目录查找逻辑）
	resolved := discoverAdbInDir(filepath.Join(in.dataDir, "platform-tools"))
	if resolved != final {
		t.Errorf("discoverAdbInDir = %q；期望 %q", resolved, final)
	}
}

func TestAdbInstallerTooLarge(t *testing.T) {
	old := maxAdbInstallBytes
	maxAdbInstallBytes = 16 // 调小上限验证限流
	defer func() { maxAdbInstallBytes = old }()

	payload := bytes.Repeat([]byte("x"), 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	in := newTestAdbInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := in.Status()
		if st.State == AdbInstallDone || st.State == AdbInstallFailed {
			if st.State != AdbInstallFailed || !strings.Contains(st.Error, "过大") {
				t.Errorf("应因超限失败: %+v", st)
			}
			// 失败不留半截文件
			if _, err := os.Stat(filepath.Join(in.dataDir, "platform-tools", adbBinaryName())); !os.IsNotExist(err) {
				t.Error("超限失败后不应留下安装文件")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("限流未触发: %+v", in.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAdbInstallerConcurrentStart(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond) // 拖住第一次安装
		_, _ = w.Write(buildPlatformToolsZip(t, adbBinaryName()))
	}))
	defer srv.Close()

	in := newTestAdbInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("第一次 Start 失败: %v", err)
	}
	// 安装中：第二次必须被拒（否则两个 goroutine 抢写同一文件）
	if err := in.Start(); err == nil {
		t.Error("安装进行中时第二次 Start 应被拒绝")
	}
	time.Sleep(600 * time.Millisecond) // 等后台收尾，避免泄漏到其他测试
}

func TestAdbInstallerNoDataDir(t *testing.T) {
	in := NewAdbInstaller("")
	if err := in.Start(); err == nil {
		t.Error("数据目录为空时应报错")
	}
}

// discoverAdbInDir 检查目录下是否存在 adb 可执行文件（测试辅助，
// 与 manager.go discoverAdb 的单目录探测逻辑一致）。
func discoverAdbInDir(dir string) string {
	cand := filepath.Join(dir, adbBinaryName())
	if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
		return cand
	}
	return ""
}
