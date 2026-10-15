package adb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"time"
)

// EnsureServer 确保 adb server 已在运行；不可达时用 adb 二进制拉起。
// adbPath 为空则从 PATH 查找。后续版本将把内置二进制释放到数据目录再执行。
func EnsureServer(ctx context.Context, adbPath string, c *Client) error {
	if c.Ping() == nil {
		return nil
	}
	path := adbPath
	if path == "" {
		p, err := exec.LookPath("adb")
		if err != nil {
			return fmt.Errorf("adb server 不可达且未找到 adb 可执行文件: %w", err)
		}
		path = p
	}
	log.Printf("[adb] 启动 adb server: %s start-server", path)
	cmd := exec.CommandContext(ctx, path, "start-server")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start-server 失败: %v: %s", err, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c.Ping() == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("adb server 启动后仍不可达")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Manager 面向 API 层的封装：server 管理 + 设备跟踪 + 常用设备操作。
type Manager struct {
	client  *Client
	tracker *Tracker
	adbPath string
}

func NewManager(adbPath, serverAddr string) *Manager {
	c := NewClient(serverAddr)
	m := &Manager{client: c, tracker: NewTracker(c), adbPath: adbPath}
	// 设备跟踪中发现 adb server 消失时自动拉起
	m.tracker.SetEnsureServer(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := EnsureServer(ctx, adbPath, c); err != nil {
			log.Printf("[adb] 自动拉起 adb server 失败: %v", err)
		} else {
			log.Printf("[adb] adb server 已自动恢复")
		}
	})
	return m
}

// Start 尝试拉起 adb server 并启动设备跟踪。server 暂不可达时返回错误，
// 跟踪循环仍会在后台持续重试。
func (m *Manager) Start(ctx context.Context) error {
	go m.tracker.Run(ctx)
	return EnsureServer(ctx, m.adbPath, m.client)
}

// Client 暴露底层 adb 客户端（供 scrcpy 等模块使用）。
func (m *Manager) Client() *Client { return m.client }

// Devices 返回当前设备快照（含记忆的掉线无线设备，状态 offline）。
func (m *Manager) Devices() []Device { return m.tracker.Devices() }

// ForgetDevice 将掉线设备从列表记忆中移除。
func (m *Manager) ForgetDevice(serial string) { m.tracker.ForgetDevice(serial) }

// Subscribe 订阅设备列表变更。
func (m *Manager) Subscribe() (<-chan []Device, func()) { return m.tracker.Subscribe() }

// Connect 连接网络设备（adb connect）。
func (m *Manager) Connect(addr string) (string, error) { return m.client.Connect(addr) }

// Disconnect 断开设备。
func (m *Manager) Disconnect(serial string) error {
	_, err := m.client.Disconnect(serial)
	return err
}

// Shell 在设备上执行命令。
func (m *Manager) Shell(serial, cmd string) (string, error) { return m.client.Shell(serial, cmd) }
