package adb

import (
	"os"
	"path/filepath"
	"runtime"
)

// AdbCandidate 是设置页「快捷切换」下拉的一个 adb 候选项。
type AdbCandidate struct {
	Path    string `json:"path"`    // 可执行文件绝对路径
	Source  string `json:"source"`  // 来源标签：installed（程序自带）/ discovered（扫描发现）/ current（当前生效）
	Version string `json:"version"` // adb version 首行（探测失败为空）
}

// CandidateDirs 返回所有可能存在 adb 的目录（去重，含数据目录的安装落点）。
// 供设置页快捷切换列出全部候选；数据目录由调用方传入。
func CandidateDirs(dataDir string) []string {
	dirs := commonAdbDirs()
	if dataDir != "" {
		dirs = append([]string{filepath.Join(dataDir, "platform-tools")}, dirs...)
	}
	return dirs
}

// ScanCandidates 扫描所有候选目录里的 adb 可执行文件。
// 每个候选都实际执行 version 探测（数量有限，目录探测本来就有 IO；
// 失败的候选项仍返回，Version 为空，界面可标注不可用）。
// current 为当前生效路径（用于打 current 标签）。
func ScanCandidates(dataDir, current string) []AdbCandidate {
	name := adbBinaryName()
	seen := map[string]bool{}
	var out []AdbCandidate
	add := func(path, source string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		c := AdbCandidate{Path: path, Source: source}
		if ver, err := ProbeAdb(path); err == nil {
			c.Version = firstLine(ver)
		}
		out = append(out, c)
	}
	// 当前生效路径永远排第一（若是用户手填的自定义路径，不在候选目录里也能列出）
	if current != "" {
		add(current, "current")
	}
	// 数据目录安装落点（一键安装产物）→ installed
	if p := firstExisting(filepath.Join(dataDir, "platform-tools", name)); p != "" {
		add(p, "installed")
	}
	// 常见安装位置 → discovered
	for _, dir := range commonAdbDirs() {
		if p := firstExisting(filepath.Join(dir, name)); p != "" {
			add(p, "discovered")
		}
	}
	return out
}

// firstExisting 返回第一个存在的普通文件路径，否则空串。
func firstExisting(p string) string {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return ""
}

// firstLine 取多行输出的第一行。
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return trimCR(s[:i])
		}
	}
	return s
}

func trimCR(s string) string {
	if runtime.GOOS == "windows" && len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}
