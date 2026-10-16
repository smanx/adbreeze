package api

import (
	"adbreeze/internal/scrcpy"
)

// controlEvent 是视频 WS 上行的控制事件（浏览器 → 服务端）。
// 坐标均为视频像素坐标（前端按 canvas 实际分辨率换算）。
type controlEvent struct {
	Type   string `json:"type"` // touch / key / text / scroll / op / ping
	Action string `json:"action,omitempty"`
	X      int    `json:"x,omitempty"`
	Y      int    `json:"y,omitempty"`
	Pid    uint64 `json:"pid,omitempty"`  // 多点触控 pointer id
	Code   uint32 `json:"code,omitempty"` // Android keycode
	Meta   uint32 `json:"meta,omitempty"`
	Text   string `json:"text,omitempty"`
	DX     int    `json:"dx,omitempty"`
	DY     int    `json:"dy,omitempty"`
	Name   string `json:"name,omitempty"` // op: back/home/menu/rotate/notify/power/volup/voldn
	T0     int64  `json:"t0,omitempty"`   // ping: 浏览器发送时刻（墙钟毫秒），时钟同步用
}

// handleControlEvent 解析并编码为 scrcpy ControlMessage 发送到设备。
func handleControlEvent(m *controlEventSink, ev *controlEvent) error {
	w, h := m.videoSize()
	sw, sh := uint16(w), uint16(h)

	var msgs [][]byte
	switch ev.Type {
	case "touch":
		var action byte = scrcpy.AMotionMove
		pressure := uint16(0xFFFF)
		switch ev.Action {
		case "down":
			action = scrcpy.AMotionDown
		case "up":
			action = scrcpy.AMotionUp
			pressure = 0
		}
		msgs = [][]byte{scrcpy.EncodeTouch(action, ev.Pid, ev.X, ev.Y, sw, sh, pressure)}

	case "key":
		var action byte = scrcpy.AKeyActionDown
		if ev.Action == "up" {
			action = scrcpy.AKeyActionUp
		}
		msgs = [][]byte{scrcpy.EncodeKeycode(action, ev.Code, 0, ev.Meta)}

	case "text":
		msg, err := scrcpy.EncodeText(ev.Text)
		if err != nil {
			return err
		}
		msgs = [][]byte{msg}

	case "scroll":
		// 前端 dy>0 = 向下滚动 → 设备端 vScroll 为负
		msgs = [][]byte{scrcpy.EncodeScroll(ev.X, ev.Y, sw, sh,
			intToFixed(float64(ev.DX)), intToFixed(float64(ev.DY)))}

	case "op":
		var kc uint32
		switch ev.Name {
		case "back":
			kc = scrcpy.KeycodeBack
		case "home":
			kc = scrcpy.KeycodeHome
		case "menu":
			kc = scrcpy.KeycodeMenu
		case "power":
			kc = scrcpy.KeycodePower
		case "volup":
			kc = scrcpy.KeycodeVolumeUp
		case "voldn":
			kc = scrcpy.KeycodeVolumeDn
		case "rotate":
			return m.send(scrcpy.EncodeSimple(scrcpy.CtlTypeRotateDevice))
		case "notify":
			return m.send(scrcpy.EncodeSimple(scrcpy.CtlTypeExpandNotifPanel))
		default:
			return nil
		}
		msgs = scrcpy.EncodeKeyTap(kc, 0)
	default:
		return nil
	}

	for _, msg := range msgs {
		if err := m.send(msg); err != nil {
			return err
		}
	}
	return nil
}

func intToFixed(v float64) float32 {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}

// controlEventSink 聚合视频尺寸与控制消息发送。
type controlEventSink struct {
	videoSize func() (int, int)
	send      func([]byte) error
}
