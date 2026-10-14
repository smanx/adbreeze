// Package adb 实现与 adb server（smart socket 协议）的原生通信，
// 覆盖设备列表、热插拔跟踪、shell 执行与网络设备连接管理。
package adb

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServerAddr = "127.0.0.1:5037"
	dialTimeout       = 3 * time.Second
)

// ErrServerNotFound 表示 adb server 不可达。
var ErrServerNotFound = errors.New("adb server not reachable")

// Client 实现 adb server 的 smart socket 客户端协议。
// 协议：每条请求为 4 字节十六进制长度 + 命令串；响应为 OKAY/FAIL + 定长 payload。
type Client struct {
	Addr string
}

func NewClient(addr string) *Client {
	if addr == "" {
		addr = defaultServerAddr
	}
	return &Client{Addr: addr}
}

// Device 是 adb 设备列表中的一项。
type Device struct {
	Serial string `json:"serial"`
	State  string `json:"state"` // device / offline / unauthorized ...
	Model  string `json:"model,omitempty"`
}

// Ping 检查 adb server 是否可达。
func (c *Client) Ping() error {
	conn, err := net.DialTimeout("tcp", c.Addr, dialTimeout)
	if err != nil {
		return ErrServerNotFound
	}
	_ = conn.Close()
	return nil
}

// ListDevices 返回当前设备列表（host:devices）。
func (c *Client) ListDevices() ([]Device, error) {
	payload, err := c.request("host:devices")
	if err != nil {
		return nil, err
	}
	return parseDevices(string(payload)), nil
}

// Connect 发起 adb connect 网络连接，返回 server 响应文本。
func (c *Client) Connect(addr string) (string, error) {
	return c.requestMessage("host:connect:" + addr)
}

// Disconnect 断开网络设备，返回 server 响应文本。
func (c *Client) Disconnect(addr string) (string, error) {
	return c.requestMessage("host:disconnect:" + addr)
}

// Forward 建立端口转发：adb forward <local> <remote>（如 tcp:27183 → localabstract:scrcpy_xxx）。
func (c *Client) Forward(serial, local, remote string) error {
	return c.hostCommand(fmt.Sprintf("host-serial:%s:forward:%s;%s", serial, local, remote))
}

// KillForward 移除指定本地转发。
func (c *Client) KillForward(serial, local string) error {
	return c.hostCommand(fmt.Sprintf("host-serial:%s:killforward:%s", serial, local))
}

// Reverse 建立反向转发：设备侧 remote（localabstract:xxx）连入主机侧 local（tcp:port）。
// 服务格式为 reverse:forward:<remote>;<local>（与官方 adb 客户端一致，
// 经 ADB_TRACE=services 实测确认），必须先 transport 切换。
func (c *Client) Reverse(serial, remote, local string) error {
	return c.transportHostCommand(serial, "reverse:forward:"+remote+";"+local)
}

// KillReverse 移除指定反向转发。
func (c *Client) KillReverse(serial, remote string) error {
	return c.transportHostCommand(serial, "reverse:remove:"+remote)
}

// transportHostCommand 切换到指定设备后执行 host 命令（OKAY 即成功）。
func (c *Client) transportHostCommand(serial, cmd string) error {
	conn, err := c.transport(serial)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeCmd(conn, cmd); err != nil {
		return err
	}
	return expectStatus(conn)
}

// hostCommand 执行无 payload 的 host 命令（OKAY 即成功）。
func (c *Client) hostCommand(cmd string) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeCmd(conn, cmd); err != nil {
		return err
	}
	return expectStatus(conn)
}

// request 发送 host 命令，返回 OKAY 之后的定长 payload。
func (c *Client) request(cmd string) ([]byte, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := writeCmd(conn, cmd); err != nil {
		return nil, err
	}
	if err := expectStatus(conn); err != nil {
		return nil, err
	}
	return readPayload(conn)
}

func (c *Client) requestMessage(cmd string) (string, error) {
	b, err := c.request(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (c *Client) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrServerNotFound, err)
	}
	return conn, nil
}

// transport 建立到指定设备的隧道连接，返回的连接可继续发送服务命令（如 shell:...）。
func (c *Client) transport(serial string) (net.Conn, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	if err := writeCmd(conn, "host:transport:"+serial); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := expectStatus(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func writeCmd(w net.Conn, cmd string) error {
	_, err := w.Write([]byte(fmt.Sprintf("%04x%s", len(cmd), cmd)))
	return err
}

func expectStatus(conn net.Conn) error {
	var status [4]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil {
		return err
	}
	switch string(status[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		msg, _ := readPayload(conn)
		return fmt.Errorf("adb: %s", msg)
	default:
		return fmt.Errorf("adb: bad status %q", status)
	}
}

func readPayload(conn net.Conn) ([]byte, error) {
	var hex [4]byte
	if _, err := io.ReadFull(conn, hex[:]); err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(string(hex[:]), 16, 32)
	if err != nil {
		return nil, fmt.Errorf("adb: bad length %q", hex)
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// parseDevices 解析 host:devices / host:devices-l 的文本输出。
func parseDevices(s string) []Device {
	var out []Device
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := Device{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] { // devices-l 格式的附加属性
			if k, v, ok := strings.Cut(f, ":"); ok && k == "model" {
				d.Model = v
			}
		}
		out = append(out, d)
	}
	return out
}
