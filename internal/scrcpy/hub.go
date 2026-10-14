package scrcpy

import "sync"

// Packet Kind 区分视频数据与流事件。
const (
	PacketVideo  uint8 = 0 // H.264 视频/配置包
	PacketResize uint8 = 1 // 视频尺寸变化（Data 为 JSON：{"width":..,"height":..}）
)

// Packet 是一帧解析后的视频包或流事件。
type Packet struct {
	Kind   uint8
	Config bool   // SPS/PPS 等配置包
	Key    bool   // 关键帧
	PTS    uint64 // 微秒
	Data   []byte // H.264 AnnexB 裸流（PacketVideo）或 JSON（PacketResize）
}

// Subscriber 是一个流订阅者（对应一个 WebSocket 客户端）。
type Subscriber struct {
	ch chan Packet
}

// Chan 返回订阅通道，通道关闭表示流结束。
func (s *Subscriber) Chan() <-chan Packet { return s.ch }

// Count 返回当前订阅者数量（用于多观众判断）。
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Hub 把一路设备流扇出给多个订阅者，并缓存最近一个配置包
// （新订阅者先补发配置包，再等第一个关键帧开始解码）。
type Hub struct {
	mu         sync.Mutex
	subs       map[*Subscriber]struct{}
	lastConfig []byte
	closed     bool
}

func NewHub() *Hub {
	return &Hub{subs: make(map[*Subscriber]struct{})}
}

// Add 注册订阅者，返回订阅者、取消函数与缓存的配置包（可能为 nil）。
func (h *Hub) Add() (*Subscriber, func(), []byte) {
	s := &Subscriber{ch: make(chan Packet, 128)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(s.ch)
		return s, func() {}, nil
	}
	h.subs[s] = struct{}{}
	var cfg []byte
	if h.lastConfig != nil {
		cfg = append([]byte(nil), h.lastConfig...)
	}
	return s, func() { h.remove(s) }, cfg
}

func (h *Hub) remove(s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
}

// Broadcast 广播一帧。消费过慢的订阅者不再整体剔除（后台标签页被浏览器节流时
// 会被误踢，表现为「另一路投屏莫名断开」），而是丢最旧保最新：视频 P 帧丢失
// 最多造成短暂花屏，i-frame-interval=2 下 2 秒内由下一个关键帧自愈。
func (h *Hub) Broadcast(p Packet) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if p.Config {
		h.lastConfig = append([]byte(nil), p.Data...)
	}
	subs := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	for _, s := range subs {
		select {
		case s.ch <- p:
		default:
			// 通道已满：丢弃最旧的一帧腾位，保住最新帧
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- p:
			default:
				// 通道被并发关闭等极端情况：仅此订阅者剔除
				h.remove(s)
			}
		}
	}
}

// Close 关闭 hub 并关闭所有订阅通道（幂等）。
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for s := range h.subs {
		delete(h.subs, s)
		close(s.ch)
	}
}
