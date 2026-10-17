package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

func TestAssetFor(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantTgz      bool
		wantOK       bool
	}{
		{"windows", "amd64", "cloudflared-windows-amd64.exe", false, true},
		{"windows", "arm64", "", false, false}, // Cloudflare 未提供
		{"linux", "amd64", "cloudflared-linux-amd64", false, true},
		{"linux", "arm64", "cloudflared-linux-arm64", false, true},
		{"linux", "arm", "cloudflared-linux-arm", false, true},
		{"linux", "386", "cloudflared-linux-386", false, true},
		{"darwin", "arm64", "cloudflared-darwin-arm64.tgz", true, true},
		{"darwin", "amd64", "cloudflared-darwin-amd64.tgz", true, true},
		{"android", "arm64", "", false, false}, // APK 内嵌场景不支持
		{"plan9", "amd64", "", false, false},
	}
	for _, c := range cases {
		name, tgz, ok := assetFor(c.goos, c.goarch)
		if name != c.want || tgz != c.wantTgz || ok != c.wantOK {
			t.Errorf("assetFor(%s/%s) = %q,%v,%v；期望 %q,%v,%v", c.goos, c.goarch, name, tgz, ok, c.want, c.wantTgz, c.wantOK)
		}
	}
}

// buildTgz 构造含指定成员的 tar.gz，供解包测试。
func buildTgz(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractMember(t *testing.T) {
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "pkg.tgz")
	data := buildTgz(t, map[string]string{
		"cloudflared":          "binary-content",
		"./docs/README.md":     "decoy",
		"sub/dir/other.bin":    "decoy2",
	})
	if err := os.WriteFile(tgzPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "cloudflared")
	if err := extractMember(tgzPath, dst, "cloudflared"); err != nil {
		t.Fatalf("extractMember 失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "binary-content" {
		t.Errorf("解包内容 = %q, %v；期望 binary-content", got, err)
	}
	// 包内没有目标成员时报错
	bad := filepath.Join(dir, "bad.tgz")
	if err := os.WriteFile(bad, buildTgz(t, map[string]string{"other": "x"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractMember(bad, dst, "cloudflared"); err == nil {
		t.Error("包内无 cloudflared 时应报错")
	}
}

// newTestInstaller 构造指向测试服务器的安装器：
// 注入无代理的 HTTP client（绕开环境代理变量）与假 smoke（不真实执行）。
func newTestInstaller(t *testing.T, srv *httptest.Server) *Installer {
	t.Helper()
	in := NewInstaller(t.TempDir())
	in.baseURL = srv.URL + "/"
	in.client = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	in.smoke = func(path string) (string, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(string(b), "FAKE-CLOUDFLARED") {
			return "", fmt.Errorf("内容不对: %q", b)
		}
		return "cloudflared version stub-1.0", nil
	}
	return in
}

func TestInstallerFullFlow(t *testing.T) {
	payload := []byte("FAKE-CLOUDFLARED-BINARY-CONTENT-0123456789")
	asset, _, ok := assetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skipf("当前平台 %s/%s 无官方资产，跳过", runtime.GOOS, runtime.GOARCH)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+asset {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	in := newTestInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	// 等安装完成
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := in.Status()
		if st.State == InstallDone || st.State == InstallFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("安装未完成: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := in.Status()
	if st.State != InstallDone {
		t.Fatalf("State = %q，期望 done（error=%q）", st.State, st.Error)
	}
	if st.Version != "cloudflared version stub-1.0" {
		t.Errorf("Version = %q", st.Version)
	}
	if st.Progress != 100 || st.Downloaded != int64(len(payload)) {
		t.Errorf("进度信息 = %d%%/%d，期望 100%%/%d", st.Progress, st.Downloaded, len(payload))
	}
	want := sha256.Sum256(payload)
	if st.SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("SHA256 = %q，期望 %q", st.SHA256, hex.EncodeToString(want[:]))
	}
	// 落点 = DataDir/bin/<binName>，且内容一致、无临时文件残留
	final := filepath.Join(in.dataDir, "bin", binName())
	got, err := os.ReadFile(final)
	if err != nil || string(got) != string(payload) {
		t.Errorf("安装文件内容异常: %q, %v", got, err)
	}
	if _, err := os.Stat(final + ".download"); !os.IsNotExist(err) {
		t.Error("临时文件未清理")
	}
	// 安装后可直接被解析到（DataDir/bin 是解析链第一站）
	resolved, err := ResolveBinary(Options{DataDir: in.dataDir})
	if err != nil || resolved != final {
		t.Errorf("安装后 ResolveBinary = %q, %v；期望 %q", resolved, err, final)
	}
}

func TestInstallerTooLarge(t *testing.T) {
	old := maxInstallBytes
	maxInstallBytes = 16 // 调小上限验证限流
	defer func() { maxInstallBytes = old }()

	payload := bytes.Repeat([]byte("x"), 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	in := newTestInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := in.Status()
		if st.State == InstallDone || st.State == InstallFailed {
			if st.State != InstallFailed || !strings.Contains(st.Error, "过大") {
				t.Errorf("应因超限失败: %+v", st)
			}
			// 失败不留半截文件
			if _, err := os.Stat(filepath.Join(in.dataDir, "bin", binName())); !os.IsNotExist(err) {
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

func TestInstallerConcurrentStart(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond) // 拖住第一次安装
		_, _ = w.Write([]byte("FAKE-CLOUDFLARED"))
	}))
	defer srv.Close()

	in := newTestInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("第一次 Start 失败: %v", err)
	}
	// 安装中：第二次必须被拒（否则两个 goroutine 抢写同一文件）
	if err := in.Start(); err == nil {
		t.Error("安装进行中时第二次 Start 应被拒绝")
	}
	time.Sleep(600 * time.Millisecond) // 等后台收尾，避免泄漏到其他测试
}

func TestInstallerNoDataDir(t *testing.T) {
	in := NewInstaller("")
	if err := in.Start(); err == nil {
		t.Error("数据目录为空时应报错")
	}
}

func TestInstallerServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	in := newTestInstaller(t, srv)
	if err := in.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := in.Status()
		if st.State == InstallFailed {
			if !strings.Contains(st.Error, "404") {
				t.Errorf("错误信息应含 HTTP 状态: %q", st.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("404 未转为失败态: %+v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
