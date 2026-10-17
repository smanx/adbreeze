package tunnel

import (
	"archive/tar"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 一键安装 cloudflared：从 Cloudflare 官方 GitHub releases 下载当前平台的
// 二进制到数据目录 bin/（自动探测的第一个位置），解包（macOS 是 .tgz）、
// 赋权、--version 冒烟后即生效，无需重启 ADBreeze。
//
// 信任模型（与 winget / brew 同源）：固定官方仓库 + HTTPS 传输 + 固定
// release 资产路径。Cloudflare 未随 release 发布校验和文件，故不做外部
// checksum 比对，改为：安装后 --version 冒烟 + 界面展示 SHA256 供人工核对。
//
// 安全约束：
//   - 下载 URL 由服务端按 GOOS/GOARCH 拼装，绝不接受调用方指定 URL——
//     否则本端点会被当成任意文件下载代理（SSRF / 内网投毒）；
//   - 大小上限 maxInstallBytes（实测最大 55MB），防磁盘打满；
//   - 先落临时文件、完成后原子改名，失败不留半截文件；
//   - 安装过程在服务端进程内完成，浏览器只发指令 + 轮询进度。

const (
	defaultBaseURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/"

	installTimeout = 10 * time.Minute
	smokeTimeout   = 15 * time.Second
)

// maxInstallBytes 是下载大小上限（变量而非常量：测试可调小验证限流逻辑）。
var maxInstallBytes int64 = 160 << 20 // 160MB，实测最大 55MB

// 安装状态机的各个状态（InstallStatus.State 的原值）。
const (
	InstallIdle        = "idle"        // 从未安装过
	InstallDownloading = "downloading" // 下载中
	InstallVerifying   = "verifying"   // 解包 / 就位 / --version 冒烟
	InstallDone        = "done"        // 安装成功
	InstallFailed      = "failed"      // 安装失败（Error 有原因）
)

// InstallStatus 是一键安装的状态快照（GET /api/settings/tunnel/install 用）。
type InstallStatus struct {
	State      string    `json:"state"`
	Progress   int       `json:"progress"` // 0-100；总长未知时为 0
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"` // 0 = 服务端未给出 Content-Length
	Path       string    `json:"path"`  // 安装落点
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	Error      string    `json:"error"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Installer 执行一次（且同时仅一次）cloudflared 下载安装。
type Installer struct {
	mu      sync.Mutex
	dataDir string
	baseURL string
	client  *http.Client
	smoke   func(path string) (string, error) // 安装后自检；测试可注入假实现
	status  InstallStatus
	running bool
}

// NewInstaller 构造安装器。dataDir 为数据目录（二进制落 <dataDir>/bin）。
func NewInstaller(dataDir string) *Installer {
	return &Installer{
		dataDir: dataDir,
		baseURL: defaultBaseURL,
		client:  &http.Client{Timeout: installTimeout},
		smoke:   defaultSmoke,
		status:  InstallStatus{State: InstallIdle},
	}
}

// assetFor 返回指定平台的官方 release 资产名；ok=false 表示该平台没有
// 官方二进制（如 windows/arm64、android），需引导用户手动安装。
// 资产形态：Windows / Linux 是裸二进制，macOS 是 .tgz（内含 cloudflared）。
func assetFor(goos, goarch string) (name string, tgz bool, ok bool) {
	switch goos {
	case "windows":
		switch goarch {
		case "amd64":
			return "cloudflared-windows-amd64.exe", false, true
		case "386":
			return "cloudflared-windows-386.exe", false, true
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "cloudflared-linux-amd64", false, true
		case "arm64":
			return "cloudflared-linux-arm64", false, true
		case "arm":
			return "cloudflared-linux-arm", false, true
		case "386":
			return "cloudflared-linux-386", false, true
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return "cloudflared-darwin-amd64.tgz", true, true
		case "arm64":
			return "cloudflared-darwin-arm64.tgz", true, true
		}
	}
	return "", false, false
}

// Status 返回安装状态快照。
func (in *Installer) Status() InstallStatus {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.status
}

// Start 启动安装（后台执行，进度经 Status 查看）。
// 已在安装中、平台不受支持、数据目录未知时返回错误。
func (in *Installer) Start() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.running {
		return errors.New("安装已在进行中，请等待当前安装完成")
	}
	if in.dataDir == "" {
		return errors.New("数据目录未知，无法确定安装位置")
	}
	name, tgz, ok := assetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("Cloudflare 未提供 %s/%s 平台的官方 cloudflared 二进制，请手动安装（winget / brew / 官方 release 页）", runtime.GOOS, runtime.GOARCH)
	}
	in.running = true
	in.status = InstallStatus{State: InstallDownloading, StartedAt: time.Now()}
	go in.run(name, tgz)
	return nil
}

// run 是安装主流程：下载 → 解包/就位 → 赋权 → 冒烟。
func (in *Installer) run(asset string, tgz bool) {
	defer func() {
		in.mu.Lock()
		in.running = false
		in.status.FinishedAt = time.Now()
		in.mu.Unlock()
	}()

	binDir := filepath.Join(in.dataDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		in.fail(fmt.Errorf("创建安装目录失败: %w", err))
		return
	}
	final := filepath.Join(binDir, binName())
	tmp := final + ".download"
	defer os.Remove(tmp) // 失败路径不留半截文件

	log.Printf("[tunnel] 一键安装 cloudflared（%s/%s）: %s", runtime.GOOS, runtime.GOARCH, in.baseURL+asset)
	if err := in.download(in.baseURL+asset, tmp); err != nil {
		in.fail(err)
		return
	}

	in.setState(InstallVerifying)
	if tgz {
		if err := extractMember(tmp, final, "cloudflared"); err != nil {
			in.fail(fmt.Errorf("解压失败: %w", err))
			return
		}
	} else if err := renameOrCopy(tmp, final); err != nil {
		in.fail(err)
		return
	}
	// Unix 需要执行位；Windows 上 chmod 只切换只读属性，0700 不含"去可写"语义，无副作用
	if err := os.Chmod(final, 0o700); err != nil {
		in.fail(fmt.Errorf("设置执行权限失败: %w", err))
		return
	}

	// 冒烟：跑一次 --version，确认下载到的真是能跑的程序
	ver, err := in.smoke(final)
	if err != nil {
		in.fail(fmt.Errorf("安装后自检失败（%s 无法执行）: %w", final, err))
		return
	}

	in.mu.Lock()
	in.status.State = InstallDone
	in.status.Progress = 100
	in.status.Path = final
	in.status.Version = ver
	in.mu.Unlock()
	log.Printf("[tunnel] cloudflared 一键安装完成: %s（%s）", final, ver)
}

// download 流式下载到 dst，同时计算 SHA256 并更新进度。
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
		return fmt.Errorf("下载失败: 服务器返回 HTTP %d（%s）", resp.StatusCode, url)
	}
	if total := resp.ContentLength; total > maxInstallBytes {
		return fmt.Errorf("文件过大（%.1f MB，上限 %.0f MB），已中止", float64(total)/1048576, float64(maxInstallBytes)/1048576)
	}

	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	hasher := sha256.New()
	counter := &progressCounter{in: in, total: resp.ContentLength}
	// LimitReader 兜底：服务端谎报 Content-Length 时也在上限处截断
	n, err := io.Copy(io.MultiWriter(f, hasher, counter), io.LimitReader(resp.Body, maxInstallBytes+1))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("下载写入失败: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("保存文件失败: %w", closeErr)
	}
	if n > maxInstallBytes {
		return fmt.Errorf("文件超过大小上限（%.0f MB），已中止", float64(maxInstallBytes)/1048576)
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

// progressCounter 按字节阈值节流更新进度，避免每个 32KB 都抢锁。
type progressCounter struct {
	in    *Installer
	total int64
	seen  int64
	last  int64
}

func (c *progressCounter) Write(p []byte) (int, error) {
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

// renameOrCopy 原子落位。rename 在 Unix 上可覆盖运行中的文件（保留旧 inode）；
// Windows 上目标被占用（隧道正用着旧版）时 rename 失败，此时退回直写，
// 仍失败则给出明确原因。
func renameOrCopy(tmp, dst string) error {
	if err := os.Rename(tmp, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return fmt.Errorf("读取下载文件失败: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil {
		return fmt.Errorf("写入 %s 失败：文件可能正被占用（隧道运行中会锁定 cloudflared），请先关闭隧道后重试，或改用 winget / brew 安装", dst)
	}
	return nil
}

// extractMember 从 tar.gz 中取出指定基名的普通文件，写到 dst。
func extractMember(tgzPath, dst, wantBase string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是合法的 gzip 文件: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != wantBase {
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("压缩包内未找到 %s", wantBase)
}

// defaultSmoke 执行 <path> --version，返回首行非空输出。
func defaultSmoke(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil && first == "" {
		return "", err
	}
	return first, nil
}

func (in *Installer) setState(state string) {
	in.mu.Lock()
	in.status.State = state
	in.mu.Unlock()
}

func (in *Installer) fail(err error) {
	in.mu.Lock()
	in.status.State = InstallFailed
	in.status.Error = err.Error()
	in.mu.Unlock()
	log.Printf("[tunnel] 一键安装失败: %v", err)
}
