package adb

import (
	"context"
	"errors"
	"log"
	"net"
	"sort"
	"sync"
	"time"
)

// Tracker 通过 host:track-devices 长连接监听设备热插拔，
// 维护设备快照并广播给订阅者。
// 额外记忆「最近在线的无线设备」：无线设备断开后（adb disconnect 或网络中断）
// 会从 adb devices 消失，这里将其以 offline 状态保留在列表中，便于一键重连。
type Tracker struct {
	client *Client
	// ensureServer 在检测到 adb server 不可达时调用（自动拉起）
	ensureServer func()

	mu      sync.RWMutex
	devices []Device
	// knownWireless 曾在线的无线设备（serial → 最后在线时间），掉线后仍展示
	knownWireless map[string]time.Time
	subs          map[chan []Device]struct{}
}

func NewTracker(c *Client) *Tracker {
	return &Tracker{client: c, subs: make(map[chan []Device]struct{}), knownWireless: map[string]time.Time{}}
}

// isWireless 判断 serial 是否为网络设备形态（IP:port）。
func isWireless(serial string) bool {
	_, _, err := net.SplitHostPort(serial)
	return err == nil
}

// ForgetDevice 手动从记忆列表移除某台掉线设备（用户点「移除」时调用）。
func (t *Tracker) ForgetDevice(serial string) {
	t.mu.Lock()
	delete(t.knownWireless, serial)
	devs := t.withRemembered()
	t.mu.Unlock()
	t.broadcast(devs)
}

// withRemembered 合并当前在线设备与掉线记忆（调用方须持锁）。
// 在线设备原样保留；记忆中的设备若不在线则以 offline 状态追加。
func (t *Tracker) withRemembered() []Device {
	out := make([]Device, len(t.devices), len(t.devices)+len(t.knownWireless))
	online := make(map[string]bool, len(t.devices))
	for i, d := range t.devices {
		out[i] = d
		if d.State == "device" && isWireless(d.Serial) {
			t.knownWireless[d.Serial] = time.Now()
		}
		online[d.Serial] = true
	}
	// 按最后在线时间排序追加掉线设备
	type lost struct {
		d  Device
		at time.Time
	}
	var lostDevs []lost
	for serial, at := range t.knownWireless {
		if !online[serial] {
			lostDevs = append(lostDevs, lost{Device{Serial: serial, State: "offline"}, at})
		}
	}
	sort.Slice(lostDevs, func(i, j int) bool { return lostDevs[i].at.After(lostDevs[j].at) })
	for _, l := range lostDevs {
		out = append(out, l.d)
	}
	return out
}

// SetEnsureServer 注入 adb server 自愈函数。
func (t *Tracker) SetEnsureServer(fn func()) { t.ensureServer = fn }

// RememberedWireless 返回记忆中（含当前在线）的无线设备 serial。
// 重启 adb server 会断开所有 TCP 设备，切换 adb 路径后据此把它们连回来。
func (t *Tracker) RememberedWireless() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.knownWireless))
	for serial := range t.knownWireless {
		out = append(out, serial)
	}
	return out
}

// Devices 返回当前设备快照（含记忆的掉线无线设备），空列表返回 [] 而非 null。
func (t *Tracker) Devices() []Device {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.withRemembered()
}

// Subscribe 注册订阅者，返回事件通道与取消函数。
func (t *Tracker) Subscribe() (<-chan []Device, func()) {
	ch := make(chan []Device, 8)
	t.mu.Lock()
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		delete(t.subs, ch)
		t.mu.Unlock()
	}
}

// Run 持续跟踪设备列表，断线自动重连，直到 ctx 取消。
func (t *Tracker) Run(ctx context.Context) {
	lastErr := ""
	for {
		err := t.trackOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if msg := err.Error(); msg != lastErr { // 避免相同错误刷屏
				log.Printf("[adb] 设备跟踪断开: %v，将自动重连", err)
				lastErr = msg
			}
			// server 不可达时自动拉起
			if errors.Is(err, ErrServerNotFound) && t.ensureServer != nil {
				t.ensureServer()
			}
		} else {
			lastErr = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (t *Tracker) trackOnce(ctx context.Context) error {
	conn, err := t.client.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeCmd(conn, "host:track-devices"); err != nil {
		return err
	}
	if err := expectStatus(conn); err != nil {
		return err
	}
	for {
		// 定期超时以便响应 ctx 取消；adb server 仅在设备列表变化时发数据
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		payload, err := readPayload(conn)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		t.update(parseDevices(string(payload)))
	}
}

func (t *Tracker) update(devs []Device) {
	t.mu.Lock()
	t.devices = devs
	merged := t.withRemembered()
	t.mu.Unlock()
	t.broadcast(merged)
}

func (t *Tracker) broadcast(devs []Device) {
	t.mu.RLock()
	subs := make([]chan []Device, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- devs:
		default: // 订阅者消费过慢，丢弃过期快照
		}
	}
}
