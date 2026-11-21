package opencode

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 一键安装 opencode：从官方更新端点下载当前平台的独立 CLI 二进制，
// 解压后落到 <DataDir>/bin/opencode，冒烟通过后即可被 Detect 找到。
// 与 adb / cloudflared 安装器同款状态机（start/status + 轮询）。

const (
	ocInstallBaseURL = "https://opencode.ai/update/api/latest/cli/opencode/"
	ocInstallTimeout = 10 * time.Minute
	ocSmokeTimeout   = 15 * time.Second
)

var maxOCInstallBytes int64 = 200 << 20 // 200MB

const (
	OCInstallIdle        = "idle"
	OCInstallDetected    = "detected" // 环境已有，无需安装
	OCInstallDownloading = "downloading"
	OCInstallVerifying   = "verifying"
	OCInstallDone        = "done"
	OCInstallFailed      = "failed"
)

// OCInstallStatus 安装状态快照。
type OCInstallStatus struct {
	State      string    `json:"state"`
	Progress   int       `json:"progress"`
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"`
	Path       string    `json:"path"`
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	Error      string    `json:"error"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Installer 管理一次（同时仅一次）opencode 安装。
type Installer struct {
	mu      sync.Mutex
	dataDir string
	baseURL string
	client  *http.Client
	status  OCInstallStatus
	running bool
}

func NewInstaller(dataDir string) *Installer {
	return &Installer{
		dataDir: dataDir,
		baseURL: ocInstallBaseURL,
		client:  &http.Client{Timeout: ocInstallTimeout},
		status:  OCInstallStatus{State: OCInstallIdle},
	}
}

func (in *Installer) Status() OCInstallStatus {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.status
}

// Start 触发安装。若环境已检测到 opencode，则只报告 detected，不下载。
func (in *Installer) Start(customPath string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.running {
		return errors.New("安装已在进行中，请等待当前安装完成")
	}
	if in.dataDir == "" {
		return errors.New("数据目录未知，无法确定安装位置")
	}
	if d := Detect(customPath); d.Installed {
		// 已安装：直接回报，不重复下载
		in.status = OCInstallStatus{
			State: OCInstallDetected, Progress: 100, Path: d.Path,
			Version: d.Version, Message: "当前环境已安装 opencode，无需重复安装",
			StartedAt: time.Now(), FinishedAt: time.Now(),
		}
		return nil
	}
	asset, err := ocAssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	in.running = true
	in.status = OCInstallStatus{State: OCInstallDownloading, StartedAt: time.Now()}
	go in.run(asset)
	return nil
}

// ocAssetFor 按平台返回官方资产文件名。
func ocAssetFor(goos, goarch string) (string, error) {
	switch goos {
	case "windows":
		switch goarch {
		case "amd64":
			return "opencode-windows-x64.zip", nil
		case "arm64":
			return "opencode-windows-arm64.zip", nil
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return "opencode-darwin-x64.zip", nil
		case "arm64":
			return "opencode-darwin-arm64.zip", nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "opencode-linux-x64.tar.gz", nil
		case "arm64":
			return "opencode-linux-arm64.tar.gz", nil
		}
	}
	return "", fmt.Errorf("官方未提供 %s/%s 平台的 opencode 独立二进制，请手动安装", goos, goarch)
}

func (in *Installer) run(asset string) {
	defer func() {
		in.mu.Lock()
		in.running = false
		in.status.FinishedAt = time.Now()
		in.mu.Unlock()
	}()

	binDir := filepath.Join(in.dataDir, "bin")
	archive := filepath.Join(binDir, "opencode-download."+extOf(asset))
	defer os.Remove(archive)
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		in.fail(fmt.Errorf("创建安装目录失败: %w", err))
		return
	}

	url := in.baseURL + asset
	log.Printf("[opencode] 一键安装: %s", url)
	if err := in.download(url, archive); err != nil {
		in.fail(err)
		return
	}

	in.setState(OCInstallVerifying)
	binName := "opencode"
	if runtime.GOOS == "windows" {
		binName = "opencode.exe"
	}
	final := filepath.Join(binDir, binName)
	if err := extractBinary(archive, final); err != nil {
		in.fail(fmt.Errorf("解压失败: %w", err))
		return
	}
	ver, err := probeVersion(final)
	if err != nil {
		in.fail(fmt.Errorf("安装后自检失败（%s 无法执行）: %w", final, err))
		return
	}

	in.mu.Lock()
	in.status.State = OCInstallDone
	in.status.Progress = 100
	in.status.Path = final
	in.status.Version = ver
	in.mu.Unlock()
	log.Printf("[opencode] 安装完成: %s (%s)", final, ver)
}

func extOf(asset string) string {
	if strings.HasSuffix(asset, ".tar.gz") {
		return "tar.gz"
	}
	if strings.HasSuffix(asset, ".zip") {
		return "zip"
	}
	return "bin"
}

func extractBinary(archive, dst string) error {
	if strings.HasSuffix(archive, ".zip") {
		return extractFromZip(archive, dst)
	}
	if strings.HasSuffix(archive, ".tar.gz") {
		return extractFromTarGz(archive, dst)
	}
	return errors.New("不支持的压缩格式")
}

func extractFromZip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		if base != "opencode" && base != "opencode.exe" {
			continue
		}
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			return fmt.Errorf("压缩包内出现可疑路径: %s", f.Name)
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return errors.New("压缩包内未找到 opencode 可执行文件")
}

func extractFromTarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(h.Name)
		if base != "opencode" && base != "opencode.exe" {
			continue
		}
		if strings.Contains(h.Name, "..") || strings.HasPrefix(h.Name, "/") {
			return fmt.Errorf("压缩包内出现可疑路径: %s", h.Name)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return errors.New("压缩包内未找到 opencode 可执行文件")
}

func (in *Installer) download(url, dst string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败: %w", err)
	}
	resp, err := in.client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxOCInstallBytes {
		return fmt.Errorf("文件过大（%.1f MB），已中止", float64(resp.ContentLength)/1048576)
	}
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxOCInstallBytes+1)
	n, err := io.Copy(io.MultiWriter(f, hasher), limited)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("保存失败: %v %v", err, closeErr)
	}
	if n > maxOCInstallBytes {
		return errors.New("文件超过大小上限，已中止")
	}
	in.mu.Lock()
	in.status.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	in.status.Total = resp.ContentLength
	in.status.Downloaded = n
	if resp.ContentLength > 0 {
		in.status.Progress = 90
	}
	in.mu.Unlock()
	return nil
}

func (in *Installer) setState(state string) {
	in.mu.Lock()
	in.status.State = state
	in.mu.Unlock()
}

func (in *Installer) fail(err error) {
	in.mu.Lock()
	in.status.State = OCInstallFailed
	in.status.Error = err.Error()
	in.mu.Unlock()
	log.Printf("[opencode] 安装失败: %v", err)
}

// SmokeVersion 供测试/外部调用执行一次版本探测。
func SmokeVersion(ctx context.Context, path string) (string, error) {
	return probeVersion(path)
}
