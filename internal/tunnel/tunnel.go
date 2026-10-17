// Package tunnel 提供外网访问能力：托管 cloudflared 子进程，把本机 ADBreeze
// 服务通过 Cloudflare Tunnel 暴露到公网。
//
// 设计要点（详见仓库根目录 CF-TUNNEL-DESIGN.md）：
//
//   - 子进程隔离：cloudflared 崩溃 / 被墙不影响本地服务；协议演进零维护；
//   - 出站连接模型：不需要公网 IP、不开路由器端口，默认打 127.0.0.1 回路，
//     与"服务是否对外监听"解耦；
//   - token 保密：只经子进程环境变量 TUNNEL_TOKEN 传递，不进 argv（ps 可见）、
//     不进日志、不进 API 响应；
//   - 配置热更新：设置页保存后 Stop→Start 应用新配置，无需重启进程；
//   - 守护：意外退出退避自动重启（1s→30s 封顶），Quick Tunnel 掉线是常态。
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// 管理器内部状态（Status.State 的原值）。
	stateIdle      = "idle"      // 从未启动过
	stateStarting  = "starting"  // 启动中（进程已拉起，URL 未就绪或重试中）
	stateRunning   = "running"   // 已连接（URL 已解析，或超时后进程仍在运行）
	stateStopped   = "stopped"   // 用户主动停止
	stateFailed    = "failed"    // 启动失败或异常退出（等待自动重启或手动重试）

	// urlTimeout：启动后多久没从输出里解析到外网地址就记 error。
	// 不杀进程——上游改 banner 格式时进程本身可能是好的，用户能从日志看到现场。
	urlTimeout = 30 * time.Second

	// stopGrace：Stop 时先发中断信号等待的时长，超时后 Kill 兜底。
	// Windows 上中断信号对非控制台子进程可能无效，直接走 Kill：
	// quick tunnel 无本地状态、强杀无损。
	stopGrace = 3 * time.Second

	// logCap：环形日志保留的行数。
	logCap = 200

	// maxBackoff：自动重启退避上限。
	maxBackoff = 30 * time.Second
)

// urlRe 匹配 quick tunnel 的外网地址。cloudflared 的创建成功横幅形如：
//
//	INF |  Your quick Tunnel has been created! Visit it at ...  |
//	INF |  https://random-words-12ab.trycloudflare.com           |
//
// 对输出行做宽容匹配，不依赖横幅的具体排版。
var urlRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Options 是启动隧道所需的全部配置。零值字段均有默认语义，
// 与 settings.TunnelConfig 一一对应（转换见 api 层）。
type Options struct {
	Binary       string // cloudflared 路径；空 = 自动探测（见 ResolveBinary）
	Service      string // 隧道指向的本地地址；空 = http://127.0.0.1:<HTTPPort>
	Protocol     string // cloudflared 传输协议；空 = 默认（quic），http2 为回退
	Token        string // Named 模式 token；空 = Quick Tunnel
	AutoRestart  bool   // 子进程意外退出后自动重启
	HTTPPort     string // 本机 HTTP 端口，Service 为空时拼默认地址用
	DataDir      string // 数据目录，探测 cloudflared 时优先找 <DataDir>/bin
}

// Mode 返回当前模式："named"（配了 token）或 "quick"。
func (o Options) Mode() string {
	if o.Token != "" {
		return "named"
	}
	return "quick"
}

// serviceURL 返回隧道实际指向的本地地址。
func (o Options) serviceURL() string {
	if o.Service != "" {
		return o.Service
	}
	return "http://127.0.0.1:" + o.HTTPPort
}

// Status 是隧道运行时状态快照（API / UI 的数据源）。
// 纯运行时态，绝不落盘——存档里只存配置与期望（见 settings.TunnelConfig）。
type Status struct {
	State      string    `json:"state"`      // idle/starting/running/stopped/failed
	Running    bool      `json:"running"`    // state == running
	Mode       string    `json:"mode"`       // quick / named（未启动时为空）
	URL        string    `json:"url"`        // quick 模式的外网地址；named 模式为空（域名在 CF 侧）
	Error      string    `json:"error"`      // 最近一次失败原因（正常时为空）
	Restarts   int       `json:"restarts"`   // 自动重启次数
	StartedAt  time.Time `json:"startedAt"`  // 本次连接建立时刻
	BinaryPath string    `json:"binaryPath"` // 实际使用的 cloudflared 路径
	Logs       []string  `json:"logs"`       // 最近 logCap 行 cloudflared 输出（含 [tunnel] 注解）
}

// Manager 托管一个 cloudflared 子进程的生命周期，并持有一键安装器。
type Manager struct {
	opts Options

	mu        sync.Mutex
	state     string
	url       string
	errMsg    string
	restarts  int
	startedAt time.Time
	binPath   string
	logs      []string // 环形缓冲，容量 logCap

	inst *Installer // 一键安装 cloudflared（落 DataDir/bin）

	stopping bool
	active   bool // supervisor 是否在跑（防重复 Start 拉起两个子进程）
	stopReq  chan struct{} // Stop 时关闭，触发 runOnce 终止进程
	done     chan struct{} // supervisor 退出时关闭，Stop 等待它
	appCtx   context.Context
}

// New 用给定配置构造 Manager（不启动）。
// ctx 必须是应用生命周期上下文（main 里由 signal.NotifyContext 创建）：
// 它被 supervisor 长期持有，不能用请求级 context。
func New(ctx context.Context, opts Options) *Manager {
	return &Manager{opts: opts, state: stateIdle, appCtx: ctx, inst: NewInstaller(opts.DataDir)}
}

// InstallStatus 返回一键安装状态快照。
func (m *Manager) InstallStatus() InstallStatus { return m.inst.Status() }

// StartInstall 启动一键安装 cloudflared（后台执行）。
func (m *Manager) StartInstall() error { return m.inst.Start() }

// Start 启动隧道。幂等：supervisor 已在运行则直接返回。
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return nil
	}
	m.stopping = false
	m.stopReq = make(chan struct{})
	m.done = make(chan struct{})
	m.state = stateStarting
	m.errMsg = ""
	m.url = ""
	m.active = true
	m.mu.Unlock()

	go m.supervise()
	return nil
}

// Stop 停止隧道并等待子进程真正退出（最多 5 秒）。幂等。
// 用户主动停止一律把状态收敛为 stopped（失败原因保留在 Error 字段供查看）。
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return
	}
	if m.done == nil {
		m.mu.Unlock() // 从未启动过
		return
	}
	m.stopping = true
	close(m.stopReq)
	done := m.done
	m.mu.Unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[tunnel] 停止超时，子进程可能未完全退出")
	}
	m.mu.Lock()
	m.state = stateStopped
	m.mu.Unlock()
}

// Restart 热重启（设置页保存配置时调用）。未在运行时只更新配置，
// 下次 Start 生效——绝不隐式开启隧道。
func (m *Manager) Restart() {
	m.mu.Lock()
	active := m.active
	ctx := m.appCtx
	m.mu.Unlock()
	if !active || ctx == nil {
		return
	}
	m.Stop()
	_ = m.Start()
}

// SetOptions 更新配置（不立即生效；运行中需配合 Restart）。
func (m *Manager) SetOptions(opts Options) {
	m.mu.Lock()
	m.opts = opts
	m.mu.Unlock()
}

// Options 返回当前配置快照（api 层合并用户设置时保留 DataDir/HTTPPort 用）。
func (m *Manager) Options() Options {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts
}

// Probe 探测 cloudflared：解析路径并取版本号（设置页展示用）。
func (m *Manager) Probe() ProbeResult {
	return Probe(m.currentOpts())
}

// Status 返回状态快照。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	logs := make([]string, len(m.logs))
	copy(logs, m.logs)
	mode := ""
	if m.state != stateIdle {
		mode = m.opts.Mode()
	}
	return Status{
		State:      m.state,
		Running:    m.state == stateRunning,
		Mode:       mode,
		URL:        m.url,
		Error:      m.errMsg,
		Restarts:   m.restarts,
		StartedAt:  m.startedAt,
		BinaryPath: m.binPath,
		Logs:       logs,
	}
}

// supervise 是守护循环：拉起子进程，退出后按策略决定重启或结束。
func (m *Manager) supervise() {
	defer func() {
		m.mu.Lock()
		m.active = false
		// ctx 取消等场景可能没走 Stop()：把残留的连接态收敛成 stopped，
		// 避免界面永远显示"启动中/已连接"而实际早已没有子进程
		if m.state == stateStarting || m.state == stateRunning {
			m.state = stateStopped
		}
		m.mu.Unlock()
		close(m.done)
	}()

	delay := time.Second
	for {
		m.runOnce()

		m.mu.Lock()
		stopping := m.stopping
		auto := m.opts.AutoRestart
		m.mu.Unlock()
		if stopping || m.appCtx.Err() != nil {
			return
		}
		if !auto {
			return
		}
		// 退避等待，可被 Stop / ctx 取消打断
		select {
		case <-m.stopReq:
			return
		case <-m.appCtx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, maxBackoff)

		m.mu.Lock()
		m.state = stateStarting
		m.restarts++
		m.mu.Unlock()
		log.Printf("[tunnel] 自动重启 cloudflared（第 %d 次）", m.restarts)
	}
}

// runOnce 跑一轮子进程：解析二进制 → 启动 → 等 URL / 等退出。
// 失败原因记入 Status.Error；由 stopping 标志区分"用户停止"与"异常退出"。
func (m *Manager) runOnce() {
	bin, err := ResolveBinary(m.currentOpts())
	if err != nil {
		m.setFailed(err)
		return
	}
	m.mu.Lock()
	m.binPath = bin
	m.mu.Unlock()

	opts := m.currentOpts()
	args := buildArgs(opts)
	// android 上经 linker64 包装执行（SELinux 禁止直接 exec 应用目录二进制）
	cmd := cloudflaredCmd(context.Background(), bin, args...)
	// token 只经环境变量传递：不进 argv（ps 可见）
	if opts.Token != "" {
		cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+opts.Token)
	}
	// 不设 Dir：cloudflared 无状态，跟本程序同工作目录即可
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.setFailed(fmt.Errorf("接管 cloudflared 输出失败: %w", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.setFailed(fmt.Errorf("接管 cloudflared 输出失败: %w", err))
		return
	}
	if err := cmd.Start(); err != nil {
		m.setFailed(fmt.Errorf("启动 cloudflared 失败: %w", err))
		return
	}

	m.mu.Lock()
	m.state = stateStarting
	m.url = ""
	m.errMsg = ""
	m.startedAt = time.Now()
	m.mu.Unlock()
	m.addLog("[tunnel] 启动: " + filepath.Base(bin) + " " + strings.Join(args, " "))

	// URL 监听 + 日志收集（cloudflared 的横幅在 stderr，stdout 也可能有）
	urlCh := make(chan string, 1)
	go scanLines(stdout, m, urlCh)
	go scanLines(stderr, m, urlCh)

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	// 阶段一：等 URL（或超时 / 被要求停止）
	var url string
	select {
	case u := <-urlCh:
		url = u
	case <-m.stopReq:
		m.terminate(cmd, waitCh)
		return
	case <-m.appCtx.Done():
		m.terminate(cmd, waitCh)
		return
	case <-time.After(urlTimeout):
		// 不杀进程：可能是上游改了横幅格式。记 error，进程继续跑
		m.mu.Lock()
		m.state = stateRunning
		m.errMsg = fmt.Sprintf("%d 秒内未解析到外网地址，cloudflared 仍在运行；请查看日志确认", int(urlTimeout.Seconds()))
		m.mu.Unlock()
		log.Printf("[tunnel] %s", m.Status().Error)
	}

	if url != "" {
		m.mu.Lock()
		m.url = url
		m.state = stateRunning
		m.errMsg = ""
		m.mu.Unlock()
		log.Printf("[tunnel] 外网地址就绪: %s", url)
	}

	// 阶段二：等进程退出（或被停止）
	select {
	case err := <-waitCh:
		m.mu.Lock()
		stopping := m.stopping
		m.mu.Unlock()
		if stopping {
			return
		}
		if err != nil {
			m.setFailed(fmt.Errorf("cloudflared 异常退出: %w", err))
			return
		}
		m.setFailed(errors.New("cloudflared 意外退出（退出码 0）"))
	case <-m.stopReq:
		m.terminate(cmd, waitCh)
	case <-m.appCtx.Done():
		m.terminate(cmd, waitCh)
	}
}

// terminate 先礼后兵：中断信号 → 等 stopGrace → Kill。
func (m *Manager) terminate(cmd *exec.Cmd, waitCh chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-waitCh:
	case <-time.After(stopGrace):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			<-waitCh
		}
	}
}

// scanLines 逐行读子进程输出：进环形日志 + 尝试解析 URL（命中一次即止）。
func scanLines(r io.Reader, m *Manager, urlCh chan string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // cloudflared 偶有长行
	for sc.Scan() {
		line := sc.Text()
		m.addLog(line)
		if loc := urlRe.FindString(line); loc != "" {
			select {
			case urlCh <- loc:
			default: // 已命中过
			}
		}
	}
}

func (m *Manager) addLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, line)
	if len(m.logs) > logCap {
		m.logs = m.logs[len(m.logs)-logCap:]
	}
}

func (m *Manager) setFailed(err error) {
	m.mu.Lock()
	m.state = stateFailed
	m.errMsg = err.Error()
	m.url = ""
	m.mu.Unlock()
	log.Printf("[tunnel] %v", err)
}

func (m *Manager) currentOpts() Options {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts
}

// buildArgs 构造 cloudflared 命令行参数。
// 注意：token 绝不进参数（只走环境变量），此处返回值可安全记入日志。
func buildArgs(opts Options) []string {
	args := []string{"tunnel", "--no-autoupdate"}
	if opts.Token != "" {
		// named 模式：入口配置由 token 决定（CF 后台），本地 service 不参与
		args = append(args, "run")
	} else {
		args = append(args, "--url", opts.serviceURL())
	}
	if opts.Protocol != "" {
		args = append(args, "--protocol", opts.Protocol)
	}
	return args
}
