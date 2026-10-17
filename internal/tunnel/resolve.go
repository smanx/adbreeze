package tunnel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// binName 是 cloudflared 可执行文件名（Windows 带 .exe）。
func binName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// ResolveBinary 按优先级解析 cloudflared 路径：
//
//	指定路径（允许填目录，自动拼接可执行名）
//	→ 数据目录 bin/（一键下载的落点，见 CF-TUNNEL-DESIGN.md §5.8 方式④）
//	→ 本程序可执行文件同目录（随包分发场景）
//	→ PATH（winget / brew / apt 安装后的常规位置）
//	→ 常见安装位置补漏（shell 未重启导致 PATH 未刷新的场景）
func ResolveBinary(opts Options) (string, error) {
	if opts.Binary != "" {
		p := opts.Binary
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			p = filepath.Join(p, binName())
		}
		if fi, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("指定的 cloudflared 路径不存在: %s", opts.Binary)
		} else if fi.IsDir() {
			return "", fmt.Errorf("指定的 cloudflared 路径是目录且其中没有 %s: %s", binName(), opts.Binary)
		} else if runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
			return "", fmt.Errorf("指定的 cloudflared 没有执行权限: %s（chmod +x 后重试）", opts.Binary)
		}
		return p, nil
	}

	// 候选位置（PATH 之外的补漏项）
	var candidates []string
	if opts.DataDir != "" {
		candidates = append(candidates, filepath.Join(opts.DataDir, "bin", binName()))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), binName()))
	}
	for _, c := range candidates {
		if isRegularFile(c) {
			return c, nil
		}
	}

	// PATH
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}

	// 常见安装位置
	for _, dir := range commonDirs() {
		c := filepath.Join(dir, binName())
		if isRegularFile(c) {
			return c, nil
		}
	}
	return "", errors.New("未找到 cloudflared：请先安装（winget install --id Cloudflare.cloudflared / brew install cloudflared），或在设置里手动指定路径")
}

// commonDirs 覆盖各平台 cloudflared 的常规安装目录。
func commonDirs() []string {
	switch runtime.GOOS {
	case "windows":
		var dirs []string
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			dirs = append(dirs, filepath.Join(la, "Microsoft", "WinGet", "Links"))
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			dirs = append(dirs, filepath.Join(pf, "cloudflared"))
		}
		return dirs
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin"}
	default: // linux 等
		return []string{"/usr/local/bin", "/usr/bin", "/snap/bin"}
	}
}

func isRegularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// MaskToken 脱敏 token：保留前 4 后 4，中间打星。
// 空 token 返回空；过短（≤8）整体打星，避免泄露有效字符。
func MaskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

// ProbeResult 是 cloudflared 探测结果（GET /api/settings/tunnel 用）。
type ProbeResult struct {
	Path    string // 解析到的路径；空 = 没找到
	Version string // cloudflared --version 首行（路径无效时为空）
	Error   string // 探测失败原因
}

// Probe 解析 cloudflared 路径并取版本号。
// 版本探测只是体验优化（设置页展示），失败不影响路径本身可用。
func Probe(opts Options) ProbeResult {
	p, err := ResolveBinary(opts)
	if err != nil {
		return ProbeResult{Error: err.Error()}
	}
	res := ProbeResult{Path: p}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "--version").CombinedOutput()
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil && first == "" {
		res.Error = fmt.Sprintf("cloudflared --version 执行失败: %v", err)
		return res
	}
	res.Version = first
	return res
}
