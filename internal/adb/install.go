package adb

import (
	"archive/zip"
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

// 一键安装 adb（platform-tools）：从 Google 官方仓库下载当前平台的 zip 到
// 数据目录 platform-tools/，解包、赋权、`adb version` 冒烟后即生效，
// 无需重启 ADBreeze。结构与 cloudflared 一键安装（internal/tunnel/install.go）一致。
//
// 信任模型：固定官方域名（dl.google.com）+ HTTPS 传输 + 固定资产路径。
// Google 不随包发布校验和，故不做 checksum 比对，改为：安装后 version 冒烟 +
// 界面展示 SHA256 供人工核对。
//
// 安全约束（与 tunnel.Installer 相同）：
//   - 下载 URL 由服务端按 GOOS 拼装，绝不接受调用方指定 URL；
//   - 大小上限防磁盘打满；
//   - 先落临时文件、完成后原子改名，失败不留半截文件；
//   - 安装在服务端进程内完成，浏览器只发指令 + 轮询进度。

const (
	adbInstallBaseURL = "https://dl.google.com/android/repository/"

	adbInstallTimeout = 10 * time.Minute
	adbSmokeTimeout   = 15 * time.Second
)

// maxAdbInstallBytes 下载大小上限（变量：测试可调小验证限流）。
var maxAdbInstallBytes int64 = 200 << 20 // 200MB，最新 platform-tools 实测 <70MB

// 安装状态机的各个状态（AdbInstallStatus.State 的原值）。
const (
	AdbInstallIdle        = "idle"        // 从未安装过
	AdbInstallDownloading = "downloading" // 下载中
	AdbInstallVerifying   = "verifying"   // 解包 / 就位 / version 冒烟
	AdbInstallDone        = "done"        // 安装成功
	AdbInstallFailed      = "failed"      // 安装失败（Error 有原因）
)

// AdbInstallStatus 是一键安装的状态快照（GET /api/settings/adb/install 用）。
type AdbInstallStatus struct {
	State      string    `json:"state"`
	Progress   int       `json:"progress"` // 0-100；总长未知时为 0
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"` // 0 = 服务端未给出 Content-Length
	Path       string    `json:"path"`  // 安装落点（adb 可执行文件）
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	Error      string    `json:"error"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// adbAssetFor 返回当前平台的官方 platform-tools 资产名；ok=false 表示该平台
// 没有可用的官方包（Google 只发 windows/darwin/linux 三种 zip，其中 Linux
// 二进制仅 amd64；darwin 包是通用二进制，arm64 可直接跑；android 端 adb
// 随 APK 资产内置，无需安装）。
func adbAssetFor(goos, goarch string) (name string, ok bool) {
	switch goos {
	case "windows":
		if goarch == "amd64" {
			return "platform-tools-latest-windows.zip", true
		}
	case "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return "platform-tools-latest-darwin.zip", true
		}
	case "linux":
		if goarch == "amd64" {
			return "platform-tools-latest-linux.zip", true
		}
	}
	return "", false
}

// AdbInstaller 执行一次（且同时仅一次）platform-tools 下载安装。
type AdbInstaller struct {
	mu      sync.Mutex
	dataDir string
	baseURL string
	client  *http.Client
	smoke   func(path string) (string, error) // 安装后自检；测试可注入假实现
	status  AdbInstallStatus
	running bool
}

// NewAdbInstaller 构造安装器。dataDir 为数据目录（zip 解到 <dataDir>/platform-tools）。
func NewAdbInstaller(dataDir string) *AdbInstaller {
	return &AdbInstaller{
		dataDir: dataDir,
		baseURL: adbInstallBaseURL,
		client:  &http.Client{Timeout: adbInstallTimeout},
		smoke:   adbInstallSmoke,
		status:  AdbInstallStatus{State: AdbInstallIdle},
	}
}

// Status 返回安装状态快照。
func (in *AdbInstaller) Status() AdbInstallStatus {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.status
}

// Start 启动安装（后台执行，进度经 Status 查看）。
// 已在安装中、平台不受支持、数据目录未知时返回错误。
func (in *AdbInstaller) Start() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.running {
		return errors.New("安装已在进行中，请等待当前安装完成")
	}
	if in.dataDir == "" {
		return errors.New("数据目录未知，无法确定安装位置")
	}
	asset, ok := adbAssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		if runtime.GOOS == "android" {
			return errors.New("安卓端 adb 已随 APK 内置，无需安装")
		}
		return fmt.Errorf("Google 未提供 %s/%s 平台的官方 platform-tools 包，请手动安装（Android Studio / platform-tools release 页）", runtime.GOOS, runtime.GOARCH)
	}
	in.running = true
	in.status = AdbInstallStatus{State: AdbInstallDownloading, StartedAt: time.Now()}
	go in.run(asset)
	return nil
}

// run 是安装主流程：下载 → 解包就位 → 赋权 → 冒烟。
func (in *AdbInstaller) run(asset string) {
	defer func() {
		in.mu.Lock()
		in.running = false
		in.status.FinishedAt = time.Now()
		in.mu.Unlock()
	}()

	// 官方 zip 顶层目录是 platform-tools/，解到 <dataDir>/platform-tools/
	// 保持内部结构（adb 与 AdbWinApi.dll 等同目录）。
	toolsDir := filepath.Join(in.dataDir, "platform-tools")
	zipPath := toolsDir + ".zip.download"
	defer os.Remove(zipPath) // 失败路径不留半截文件

	url := in.baseURL + asset
	log.Printf("[adb] 一键安装 platform-tools（%s/%s）: %s", runtime.GOOS, runtime.GOARCH, url)
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o700); err != nil {
		in.fail(fmt.Errorf("创建安装目录失败: %w", err))
		return
	}
	if err := in.download(url, zipPath); err != nil {
		in.fail(err)
		return
	}

	in.setState(AdbInstallVerifying)
	adbPath, err := extractPlatformTools(zipPath, toolsDir)
	if err != nil {
		in.fail(fmt.Errorf("解压失败: %w", err))
		return
	}

	// 冒烟：跑一次 version，确认下载到的真是能跑的 adb
	ver, err := in.smoke(adbPath)
	if err != nil {
		in.fail(fmt.Errorf("安装后自检失败（%s 无法执行）: %w", adbPath, err))
		return
	}

	in.mu.Lock()
	in.status.State = AdbInstallDone
	in.status.Progress = 100
	in.status.Path = adbPath
	in.status.Version = ver
	in.mu.Unlock()
	log.Printf("[adb] platform-tools 一键安装完成: %s（%s）", adbPath, ver)
}

// extractPlatformTools 解开官方 zip：取顶层 platform-tools/ 目录下所有普通文件
// 写入 dstDir（保留相对路径），返回 adb 可执行文件路径。覆盖式解包——重复执行
// 即升级到最新版。zip 顶层目录名容错：不写死 platform-tools，取首个条目的顶层段。
func extractPlatformTools(zipPath, dstDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("不是合法的 zip 文件: %w", err)
	}
	defer zr.Close()

	top := ""
	adbName := "adb"
	if runtime.GOOS == "windows" {
		adbName = "adb.exe"
	}
	adbPath := ""
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		// 只取 platform-tools 顶层目录下的内容，拒 zip slip（.. / 绝对路径）
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return "", fmt.Errorf("zip 内出现可疑路径: %s", f.Name)
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue // 顶层散文件（正常包里没有），跳过
		}
		if top == "" {
			top = parts[0]
		} else if parts[0] != top {
			continue // 非同顶层目录的条目，跳过
		}
		dst := filepath.Join(dstDir, filepath.FromSlash(parts[1]))
		if err := copyZipFile(f, dst); err != nil {
			return "", err
		}
		if filepath.Base(dst) == adbName {
			adbPath = dst
		}
	}
	if adbPath == "" {
		return "", fmt.Errorf("压缩包内未找到 %s", adbName)
	}
	return adbPath, nil
}

// copyZipFile 把 zip 条目写到 dst（自动建父目录，权限 0o755）。
func copyZipFile(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// download 流式下载到 dst，同时计算 SHA256 并更新进度。
func (in *AdbInstaller) download(url, dst string) error {
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
		return fmt.Errorf("下载失败: 服务器返回 HTTP %d（%s）", resp.StatusCode, url)
	}
	if total := resp.ContentLength; total > maxAdbInstallBytes {
		return fmt.Errorf("文件过大（%.1f MB，上限 %.0f MB），已中止", float64(total)/1048576, float64(maxAdbInstallBytes)/1048576)
	}

	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	hasher := sha256.New()
	counter := &adbProgressCounter{in: in, total: resp.ContentLength}
	// LimitReader 兜底：服务端谎报 Content-Length 时也在上限处截断
	n, err := io.Copy(io.MultiWriter(f, hasher, counter), io.LimitReader(resp.Body, maxAdbInstallBytes+1))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("下载写入失败: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("保存文件失败: %w", closeErr)
	}
	if n > maxAdbInstallBytes {
		return fmt.Errorf("文件超过大小上限（%.0f MB），已中止", float64(maxAdbInstallBytes)/1048576)
	}

	in.mu.Lock()
	in.status.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	in.status.Total = resp.ContentLength
	in.status.Downloaded = n
	if resp.ContentLength > 0 {
		in.status.Progress = int(n * 100 / resp.ContentLength)
	}
	in.mu.Unlock()
	return nil
}

// adbProgressCounter 按字节阈值节流更新进度，避免每个 32KB 都抢锁。
type adbProgressCounter struct {
	in    *AdbInstaller
	total int64
	seen  int64
	last  int64
}

func (c *adbProgressCounter) Write(p []byte) (int, error) {
	n := len(p)
	c.seen += int64(n)
	if c.seen-c.last >= 512*1024 || c.seen == c.total {
		c.last = c.seen
		c.in.mu.Lock()
		c.in.status.Downloaded = c.seen
		c.in.status.Total = c.total
		if c.total > 0 {
			c.in.status.Progress = int(c.seen * 100 / c.total)
		}
		c.in.mu.Unlock()
	}
	return n, nil
}

// adbInstallSmoke 执行 <path> version，返回首行非空输出。
// android 上经 linker64 包装（与 ExecUtil/壳侧同款限制，见 manager.go）。
func adbInstallSmoke(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), adbSmokeTimeout)
	defer cancel()
	out, err := adbCmd(ctx, path, "version").CombinedOutput()
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil && first == "" {
		return "", err
	}
	return first, nil
}

func (in *AdbInstaller) setState(state string) {
	in.mu.Lock()
	in.status.State = state
	in.mu.Unlock()
}

func (in *AdbInstaller) fail(err error) {
	in.mu.Lock()
	in.status.State = AdbInstallFailed
	in.status.Error = err.Error()
	in.mu.Unlock()
	log.Printf("[adb] 一键安装失败: %v", err)
}
