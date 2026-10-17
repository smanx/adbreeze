// Package settings 持久化用户可运行时修改的偏好（目前是 adb 可执行文件路径）。
//
// 之所以单独存一份而不是塞进 Config：Config 来自命令行，进程启动后不再变化；
// 这里的值要能在网页设置面板里随时改，且重启进程后仍然生效。
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Settings 是可持久化的用户偏好。字段增删需保持向后兼容：
// 旧版本存档缺字段时按零值处理，因此解析失败只记录不致命。
type Settings struct {
	// AdbPath 为 adb 可执行文件路径；空字符串表示使用默认（从 PATH 查找）。
	// 允许填目录，实际可执行文件在解析时拼接（见 adb.ResolveAdbPath）。
	AdbPath string `json:"adbPath"`

	// Tunnel 为外网访问（Cloudflare Tunnel）配置。见 internal/tunnel。
	// token 是敏感凭证，故整个存档写盘权限收紧到 0600（见 Set）。
	Tunnel TunnelConfig `json:"tunnel"`
}

// TunnelConfig 是设置页「外网访问」卡片可改、重启进程仍生效的隧道配置。
// 与运行时状态（是否正在连接、URL 等，见 tunnel.Status）严格分层：
// 这里只存"配置与期望"，绝不存运行态——否则重启后界面会假显示已连接。
// 所有字段零值均有明确定义（空 = 自动探测 / 默认行为），旧存档缺整个
// tunnel 对象时按零值处理，天然向后兼容。
type TunnelConfig struct {
	// Binary 为 cloudflared 可执行文件路径；空 = 自动探测
	// （数据目录 bin/ → 本程序同目录 → PATH → 常见安装位置）。
	Binary string `json:"binary"`
	// Service 为隧道指向的本地地址；空 = http://127.0.0.1:<HTTP 端口>。
	Service string `json:"service"`
	// Protocol 为 cloudflared 传输协议；空 = 默认（quic），http2 为回退。
	Protocol string `json:"protocol"`
	// Token 为 Named Tunnel 令牌（CF 后台获取）；空 = Quick Tunnel 模式。
	// 明文落盘（0600），GET 接口只回脱敏预览，子进程只经环境变量接收。
	Token string `json:"token"`
	// AutoRestart 控制子进程意外退出后是否自动重启（默认 true，
	// 用指针区分"未设置"与"显式 false"，旧存档缺字段按 true 处理）。
	AutoRestart *bool `json:"autoRestart,omitempty"`
	// Resume 记住上次运行状态：true = 随 ADBreeze 启动自动恢复隧道。
	// 由用户动作维护——每次「开启」（含 -tunnel  flag）置 true，
	// 每次手动「关闭」置 false；从未开启过的存档缺省 false。
	// 即：开启过且没手动关闭 → 下次自启；从未开启或手动关闭过 → 不自启。
	Resume *bool `json:"resume,omitempty"`
	// LegacyAutoStart 是旧版手动开关字段（语义已被 Resume 取代），
	// 仅存在于一次性迁移：load 时若 resume 缺省而它存在，取其值为
	// resume 初值并停止写出；之后存档里只剩 resume。
	LegacyAutoStart *bool `json:"autoStart,omitempty"`
}

// AutoRestartEnabled 返回自动重启开关，缺省（旧存档）视为开启。
func (c TunnelConfig) AutoRestartEnabled() bool {
	return c.AutoRestart == nil || *c.AutoRestart
}

// ResumeEnabled 返回是否随启动自动恢复隧道，缺省（旧存档 / 从未开启）为 false。
func (c TunnelConfig) ResumeEnabled() bool {
	if c.Resume != nil {
		return *c.Resume
	}
	if c.LegacyAutoStart != nil { // 一次性迁移路径
		return *c.LegacyAutoStart
	}
	return false
}

// Store 以文件为后端保存设置，读写加锁（HTTP 并发访问）。
type Store struct {
	path string // 存档文件绝对路径
	mu   sync.RWMutex
	cur  Settings
}

// defaultPath 返回默认存档位置：用户配置目录下的 ADBreeze/settings.json。
// 不用项目的 DataDir（默认落在系统临时目录，重启机器会被清理），
// 用户偏好属于长期数据，按约定放用户配置目录。
func defaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ADBreeze", "settings.json"), nil
}

// Open 加载存档（不存在时返回空设置，不报错）并返回 Store。
// 定位配置目录失败时返回错误（调用方据错误日志降级启动）。
func Open() (*Store, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, fmt.Errorf("定位配置目录失败: %w", err)
	}
	s := &Store{path: p}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewInMemory 返回不落盘的 Store（配置目录不可用时兜底）：
// 功能仍可用，只是改动在进程重启后丢失。
func NewInMemory() *Store { return &Store{} }

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // 首次运行：无存档即用默认值
		}
		return fmt.Errorf("读取设置失败: %w", err)
	}
	var v Settings
	if err := json.Unmarshal(data, &v); err != nil {
		// 坏存档不阻断启动：当作默认值，并保留原文件供排查
		return nil
	}
	// 一次性迁移：旧手动开关 autoStart → 记忆字段 resume。
	// resume 缺省而 autoStart 存在时取其值；迁移后 legacy 字段不再写出，
	// 存档里最终只剩 resume（下次保存即固化）。
	if v.Tunnel.Resume == nil && v.Tunnel.LegacyAutoStart != nil {
		v.Tunnel.Resume = v.Tunnel.LegacyAutoStart
		v.Tunnel.LegacyAutoStart = nil
	}
	s.cur = v
	return nil
}

// Get 返回当前设置的副本。
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set 写入并落盘。写盘失败时返回错误，内存值不更新，避免出现
// 「界面显示已保存、重启后丢失」的错觉。path 为空（内存态）时只更新内存。
//
// 存档内含敏感凭证（隧道 token），权限收紧到 0600：写盘用 0600 创建，
// 已存在的旧文件（可能是 0644）再 chmod 兜底——Unix 上两者都生效；
// Windows 上 chmod 只切换只读属性，0600/0644 均不含"去除可写"语义，无副作用。
func (s *Store) Set(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		s.cur = v
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("写入设置失败: %w", err)
	}
	// WriteFile 的权限参数只在创建新文件时生效；已存在文件保持原权限，需显式收敛
	if err := os.Chmod(s.path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// 权限收敛失败不阻断保存（内容已落盘），但记录下来供排查
		log.Printf("[settings] 收紧存档权限失败（内容已保存）: %v", err)
	}
	s.cur = v
	return nil
}

// FilePath 返回存档文件路径（供设置页展示，便于用户排查）。
func (s *Store) FilePath() string { return s.path }
