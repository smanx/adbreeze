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
func (s *Store) Set(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		s.cur = v
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("写入设置失败: %w", err)
	}
	s.cur = v
	return nil
}

// FilePath 返回存档文件路径（供设置页展示，便于用户排查）。
func (s *Store) FilePath() string { return s.path }
