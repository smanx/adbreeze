package adb

import (
	"fmt"
	"io"
	"net"
	"time"
)

// Shell 在指定设备上执行一次性 shell 命令，返回完整输出（默认 10s 超时）。
func (c *Client) Shell(serial, cmd string) (string, error) {
	return c.ShellTimeout(serial, cmd, 10*time.Second)
}

// ShellConn 执行 shell 命令但不断开，返回原始连接（调用方负责关闭）。
// 适用于长驻进程（如 scrcpy server）：进程输出持续可读，连接关闭即终止。
func (c *Client) ShellConn(serial, cmd string) (net.Conn, error) {
	conn, err := c.transport(serial)
	if err != nil {
		return nil, err
	}
	if err := writeCmd(conn, "shell:"+cmd); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := expectStatus(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// ShellPtyConn 执行 shell 命令并分配 PTY（"shell,pty:" 服务），返回原始
// 双向流（调用方负责关闭）。cmd 为空时启动交互式登录 shell。
// 输出包含 ANSI 转义序列，适合 xterm.js 等终端前端直接渲染。
func (c *Client) ShellPtyConn(serial, cmd string) (net.Conn, error) {
	service := "shell,pty:" + cmd
	conn, err := c.transport(serial)
	if err != nil {
		return nil, err
	}
	if err := writeCmd(conn, service); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := expectStatus(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// ShellTimeout 同 Shell，可自定义超时。超时或连接中断时返回已读到的部分输出。
func (c *Client) ShellTimeout(serial, cmd string, timeout time.Duration) (string, error) {
	conn, err := c.transport(serial)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := writeCmd(conn, "shell:"+cmd); err != nil {
		return "", err
	}
	if err := expectStatus(conn); err != nil {
		return "", err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	out, err := io.ReadAll(conn)
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("adb shell: %w", err)
	}
	return string(out), nil
}
