package tunnel

import (
	"os"
	"os/exec"
	"path/filepath"
)

// TunnelCandidate 是设置页「快捷切换」下拉的一个 cloudflared 候选项。
type TunnelCandidate struct {
	Path    string `json:"path"`    // 可执行文件绝对路径
	Source  string `json:"source"`  // 来源：installed（一键安装）/ discovered（扫描发现）/ current（当前生效/指定）
	Version string `json:"version"` // --version 首行（探测失败为空）
}

// ScanCandidates 扫描所有可能存在 cloudflared 的位置（与 ResolveBinary 的
// 探测链一致，外加数据目录安装落点）。每个候选项实际执行 --version 探测，
// 失败的仍返回（Version 空）。current 为当前存档的路径（空=自动探测）。
func ScanCandidates(dataDir, current string) []TunnelCandidate {
	opts := Options{Binary: current, DataDir: dataDir}
	seen := map[string]bool{}
	var out []TunnelCandidate
	add := func(path, source string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		c := TunnelCandidate{Path: path, Source: source}
		if p := Probe(Options{Binary: path, DataDir: dataDir}); p.Error == "" {
			c.Version = p.Version
		}
		out = append(out, c)
	}
	// 当前指定路径排第一（用户手填的自定义路径不在候选位置里也能列出）
	if p := resolveOnly(opts); p != "" {
		add(p, "current")
	}
	// 数据目录安装落点 → installed
	if p := firstRegularFile(filepath.Join(dataDir, "bin", binName())); p != "" {
		add(p, "installed")
	}
	// 可执行文件同目录 → discovered（随包分发场景）
	if exe, err := os.Executable(); err == nil {
		if p := firstRegularFile(filepath.Join(filepath.Dir(exe), binName())); p != "" {
			add(p, "discovered")
		}
	}
	// PATH → discovered
	if p, err := exec.LookPath(binName()); err == nil {
		add(p, "discovered")
	}
	// 常见安装位置 → discovered
	for _, dir := range commonDirs() {
		if p := firstRegularFile(filepath.Join(dir, binName())); p != "" {
			add(p, "discovered")
		}
	}
	return out
}

// resolveOnly 只解析路径不执行版本探测（ResolveBinary 在 current 无效时会报错，
// 这里把「当前指定路径存在」这一事实单独拿出来，避免无效路径阻断整个列表）。
func resolveOnly(opts Options) string {
	if opts.Binary == "" {
		return ""
	}
	p := opts.Binary
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		p = filepath.Join(p, binName())
	}
	return firstRegularFile(p)
}

func firstRegularFile(p string) string {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}
