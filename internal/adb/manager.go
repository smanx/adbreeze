package adb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// EnsureServer 确保 adb server 已在运行；不可达时用 adb 二进制拉起。
// adbPath 为空则从 PATH 查找。
func EnsureServer(ctx context.Context, adbPath string, c *Client) error {
	if c.Ping() == nil {
		return nil
	}
	path, err := ResolveAdbPath(adbPath)
	if err != nil {
		return err
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

// adbBinaryName 返回当前平台下 adb 可执行文件名（Windows 带 .exe）。
func adbBinaryName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}

// ResolveAdbPath 把用户填写的 adb 路径解析为可直接执行的二进制路径。
//
// 用户可能填的是目录（如 D:\platform-tools）而不是可执行文件本身，
// 这里做一次归一化：目录则拼上平台对应的文件名。空值走 PATH 查找，
// PATH 找不到再补探常见安装位置（见 discoverAdb）。
func ResolveAdbPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if p, err := exec.LookPath("adb"); err == nil {
			return p, nil
		}
		if p := discoverAdb(); p != "" {
			return p, nil
		}
		return "", errors.New("adb server 不可达且未找到 adb 可执行文件（PATH 与常见安装位置均无）")
	}
	if fi, err := os.Stat(raw); err == nil && fi.IsDir() {
		raw = filepath.Join(raw, adbBinaryName())
	}
	if _, err := os.Stat(raw); err != nil {
		return "", fmt.Errorf("adb 可执行文件不存在: %s", raw)
	}
	return raw, nil
}

// commonAdbDirs 返回 PATH 之外 adb 的常见安装位置（目录，不含可执行文件名）。
//
// Windows 上 adb 极少被加进 PATH，但装了 Android Studio / scrcpy 之后位置基本固定；
// macOS / Linux 各有标准 SDK 目录。仅在 PATH 查找失败时补漏，
// PATH 能找到时行为与以前完全一致。
func commonAdbDirs() []string {
	var dirs []string
	add := func(dir string) {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	// 用户显式配置过的 SDK 根目录优先（ANDROID_HOME 是老写法，ANDROID_SDK_ROOT 是新写法）
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := strings.TrimSpace(os.Getenv(env)); root != "" {
			add(filepath.Join(root, "platform-tools"))
		}
	}
	// 与可执行文件同级的 platform-tools：用户把 adb 解压到旁边即可用
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), "platform-tools"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		switch runtime.GOOS {
		case "windows":
			add(filepath.Join(home, "AppData", "Local", "Android", "Sdk", "platform-tools"))
			add(filepath.Join(home, "platform-tools"))
		case "darwin":
			add(filepath.Join(home, "Library", "Android", "sdk", "platform-tools"))
		case "linux":
			add(filepath.Join(home, "Android", "Sdk", "platform-tools"))
		}
	}
	if runtime.GOOS == "windows" {
		// Windows 上 LOCALAPPDATA 可能被重定向，与 home 推导的不是同一个路径
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			add(filepath.Join(la, "Android", "Sdk", "platform-tools"))
		}
	}
	return dirs
}

// discoverAdb 在常见安装位置里找 adb，返回第一个存在的可执行文件路径，找不到返回空串。
// 找到时打一次日志告知用户（PATH 之外找到的，来源不显然）。
// 只在 server 不可达时才会走到这里（EnsureServer 的 Ping 会先挡掉正常路径），
// 调用频率低，直接每次实探，不缓存——用户装完 SDK 不用重启也能接着用。
func discoverAdb() string {
	name := adbBinaryName()
	for _, dir := range commonAdbDirs() {
		cand := filepath.Join(dir, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			log.Printf("[adb] PATH 中未找到 adb，使用常见安装位置: %s", cand)
			return cand
		}
	}
	return ""
}

// ProbeAdb 校验一个 adb 路径是否真的可用：解析成功且能执行 `version`。
// 设置面板「保存并测试」用它给出即时反馈，避免存下一个跑不通的路径。
func ProbeAdb(raw string) (string, error) {
	path, err := ResolveAdbPath(raw)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("执行 %s version 失败: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// RunAdb 以给定路径执行一条 adb 命令（已解析路径），返回命令输出。
// 供切换 adb 路径时执行 kill-server / start-server 使用。
func RunAdb(ctx context.Context, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adb %s 失败: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Manager 面向 API 层的封装：server 管理 + 设备跟踪 + 常用设备操作。
type Manager struct {
	client  *Client
	tracker *Tracker

	// mu 保护 adbPath：设置页可在运行时切换 adb 可执行文件
	mu      sync.RWMutex
	adbPath string
}

func NewManager(adbPath, serverAddr string) *Manager {
	c := NewClient(serverAddr)
	m := &Manager{client: c, tracker: NewTracker(c), adbPath: adbPath}
	// 设备跟踪中发现 adb server 消失时自动拉起（读当前生效路径，支持运行时切换）
	m.tracker.SetEnsureServer(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := EnsureServer(ctx, m.AdbPath(), c); err != nil {
			log.Printf("[adb] 自动拉起 adb server 失败: %v", err)
		} else {
			log.Printf("[adb] adb server 已自动恢复")
		}
	})
	return m
}

// AdbPath 返回当前生效的 adb 可执行文件路径（空 = 从 PATH 查找）。
func (m *Manager) AdbPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adbPath
}

// SetAdbPath 切换 adb 可执行文件路径。
// 仅改路径不重启已运行的 server：adb server 与路径并不绑定（同一台机器上
// 不同 platform-tools 版本共用 5037 端口）。真正生效发生在下次拉起 server 时；
// 需要立刻换掉运行中的 server 时由调用方显式重启（见 api 层的切换接口）。
func (m *Manager) SetAdbPath(p string) {
	m.mu.Lock()
	m.adbPath = p
	m.mu.Unlock()
}

// EnsureServerNow 用当前生效路径立即拉起/校验 adb server，返回错误供接口透出。
func (m *Manager) EnsureServerNow(ctx context.Context) error {
	return EnsureServer(ctx, m.AdbPath(), m.client)
}

// ReconnectWireless 重新连接此前记忆的无线设备。
// 重启 adb server 会断开所有 TCP 设备（这是 adb 固有行为，USB 设备不受影响），
// 切换 adb 路径后调用它把设备接回来，避免用户看到一片 offline 需要手动重连。
// 返回成功获得响应的 serial 列表。
//
// 不做「已在线就跳过」的预判：server 刚重启时 tracker 快照是旧的（设备仍显示
// device），据此跳过反而会把真正断掉的设备漏掉。adb connect 对已连接设备是幂等的
// （返回 already connected），重复调用无害。
func (m *Manager) ReconnectWireless() []string {
	var done []string
	for _, serial := range m.tracker.RememberedWireless() {
		if _, err := m.client.Connect(serial); err == nil {
			done = append(done, serial)
		} else {
			log.Printf("[adb] 重连无线设备失败 %s: %v", serial, err)
		}
	}
	return done
}

// Start 尝试拉起 adb server 并启动设备跟踪。server 暂不可达时返回错误，
// 跟踪循环仍会在后台持续重试。
func (m *Manager) Start(ctx context.Context) error {
	go m.tracker.Run(ctx)
	return EnsureServer(ctx, m.AdbPath(), m.client)
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
