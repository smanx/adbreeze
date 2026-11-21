// Package opencode 负责：检测/安装 opencode CLI、读写其配置（模型 / mobile-mcp）、
// 以及以子进程方式执行 `opencode run <prompt>`（定时任务的执行内核）。
package opencode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	defaultRunTimeout = 10 * time.Minute
	maxOutputBytes    = 64 * 1024 // 执行输出上限，防止撑爆记录
)

// opencode 即使设置了 NO_COLOR=1 仍会输出少量 ANSI 控制序列（颜色、光标、OSC 标题），
// 存进执行记录前统一剥离，否则用户在「执行记录」里看到的是一堆 [0m 乱码。
var ansiRE = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[()][A-Z0-9])`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// Detector 描述当前 opencode 可用性。
type Detector struct {
	Path       string `json:"path"`      // 解析到的可执行文件路径
	Version    string `json:"version"`   // --version 首行
	Installed  bool   `json:"installed"` // 是否检测到
	ConfigPath string `json:"configPath"`
	Model      string `json:"model"`
}

// Detect 检测当前环境中的 opencode：优先自定义路径，其次 PATH。
func Detect(customPath string) Detector {
	d := Detector{ConfigPath: ConfigPath(), Model: ReadModel()}
	candidates := []string{}
	if customPath != "" {
		candidates = append(candidates, customPath)
	}
	if p, err := exec.LookPath("opencode"); err == nil {
		candidates = append(candidates, p)
	}
	// 数据目录 bin/ 下的安装落点
	if dataDir := os.Getenv("ADBREEZE_DATA_DIR"); dataDir != "" {
		name := "opencode"
		if runtime.GOOS == "windows" {
			name = "opencode.exe"
		}
		candidates = append(candidates, filepath.Join(dataDir, "bin", name))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		ver, err := probeVersion(c)
		if err == nil {
			d.Installed = true
			d.Path = c
			d.Version = ver
			return d
		}
	}
	return d
}

func probeVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil {
		return "", err
	}
	if first == "" {
		return "", errors.New("--version 无输出")
	}
	return first, nil
}

// Runner 执行一次 prompt。
type Runner struct {
	binary   string
	workRoot string
	timeout  time.Duration
}

// NewRunner 构造执行器。binary 为空时运行时自动探测。
func NewRunner(binary, workRoot string, timeout time.Duration) *Runner {
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	return &Runner{binary: binary, workRoot: workRoot, timeout: timeout}
}

// RunResult 是一次执行的原始结果。
type RunResult struct {
	Output   string
	ExitCode int
	TimedOut bool
	Err      error
}

// Run 执行 prompt，输出截断到 maxOutputBytes；超时则 kill 并标记 TimedOut。
func (r *Runner) Run(ctx context.Context, taskID, prompt, model string) RunResult {
	bin := r.binary
	if bin == "" {
		d := Detect("")
		if !d.Installed {
			return RunResult{Err: errors.New("未检测到 opencode，请先在设置中安装")}
		}
		bin = d.Path
	}

	workDir := r.workRoot
	if taskID != "" {
		workDir = filepath.Join(r.workRoot, sanitizeID(taskID))
	}
	if workDir != "" {
		_ = os.MkdirAll(workDir, 0o700)
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// --auto：定时任务无人值守，自动批准工具权限，否则 Agent 一请求授权
	// 就会卡在权限确认上，直到 10 分钟超时都没有结果。
	args := []string{"run", "--auto"}
	if model != "" {
		args = append(args, "--model", model)
	}
	// prompt 走 stdin，而不是命令行参数：Windows 上 opencode 是 .cmd 启动脚本，
	// Go 会经 cmd.exe 转发参数，多行文本和【】等字符会被截断/改写，
	// 之前任务指令丢失就是这么来的（opencode run 不传 message 时会读 stdin）。
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = workDir
	// 继承环境，追加避免 ANSI 干扰输出可读性
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

	var buf limitedBuffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()

	res := RunResult{Output: stripANSI(buf.String())}
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.Err = fmt.Errorf("执行超时（%s）", r.timeout)
		return res
	}
	if ctx.Err() != nil {
		res.Err = ctx.Err()
		return res
	}
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
		if err.Error() != "" {
			res.Err = errors.New(strings.TrimSpace(res.Output))
		}
	} else if err != nil {
		res.Err = err
	}
	if res.Err == nil && strings.TrimSpace(res.Output) == "" {
		res.Err = errors.New("opencode run 无输出")
	}
	return res
}

// limitedBuffer 最多保留 maxOutputBytes 字节。
type limitedBuffer struct {
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remain := maxOutputBytes - b.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		p = p[:remain]
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}
