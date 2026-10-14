package adb

import (
	"context"
	"errors"
	"log"
	"net"
	"sync"
	"time"
)

// Tracker 通过 host:track-devices 长连接监听设备热插拔，
// 维护设备快照并广播给订阅者。
type Tracker struct {
	client *Client
	// ensureServer 在检测到 adb server 不可达时调用（自动拉起）
	ensureServer func()

	mu      sync.RWMutex
	devices []Device
	subs    map[chan []Device]struct{}
}

func NewTracker(c *Client) *Tracker {
	return &Tracker{client: c, subs: make(map[chan []Device]struct{})}
}

// SetEnsureServer 注入 adb server 自愈函数。
func (t *Tracker) SetEnsureServer(fn func()) { t.ensureServer = fn }

// Devices 返回当前设备快照（空列表返回 [] 而非 null）。
func (t *Tracker) Devices() []Device {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Device, 0, len(t.devices))
	return append(out, t.devices...)
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
	subs := make([]chan []Device, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- devs:
		default: // 订阅者消费过慢，丢弃过期快照
		}
	}
}
