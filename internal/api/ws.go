package api

import (
	"encoding/binary"
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"

	"adbreeze/internal/adb"
	"adbreeze/internal/scrcpy"
)

// upgrader 允许任意 Origin：服务默认只在内网使用，公网部署时应在反代层收敛。
var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// handleEvents 推送设备列表变更，每条消息: {"type":"devices","devices":[...]}。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch, cancel := s.mgr.Subscribe()
	defer cancel()

	// 读协程：检测客户端主动断开
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	send := func(devs []adb.Device) bool {
		return conn.WriteJSON(map[string]any{"type": "devices", "devices": devs}) == nil
	}

	if !send(s.mgr.Devices()) { // 立即推送当前快照
		return
	}
	for {
		select {
		case devs := <-ch:
			if !send(devs) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// handleVideo 推送设备投屏流并接收控制事件。
//
// 服务端 → 客户端：
//   - 第一条 JSON 文本消息: {"type":"meta","serial":...,"device":...,"codec":"h264","width":...,"height":...}
//   - 二进制帧: [1B flags( bit0=config, bit1=key )][8B PTS u64 BE][H.264 AnnexB 载荷]
//   - 尺寸变化时发送 JSON 文本消息: {"type":"resize","width":..,"height":..}
//
// 客户端 → 服务端：JSON 控制事件（touch/key/text/scroll/op，见 control.go）。
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	sub, cancel, cachedConfig, info, err := s.mirror.Subscribe(serial)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer cancel()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type":   "meta",
		"serial": info.Serial,
		"device": info.DeviceName,
		"codec":  info.Codec,
		"width":  info.Width,
		"height": info.Height,
	}); err != nil {
		return
	}

	// 补发缓存的配置包，让后加入的观看者尽快可解码
	if cachedConfig != nil {
		if !sendPacket(conn, scrcpy.Packet{Kind: scrcpy.PacketVideo, Config: true, Data: cachedConfig}) {
			return
		}
	}

	// 读协程：控制事件（JSON）→ ControlMessage → 设备
	go func() {
		sink := &controlEventSink{
			videoSize: func() (int, int) {
				w, h, err := s.mirror.VideoSize(serial)
				if err != nil {
					return 0, 0
				}
				return w, h
			},
			send: func(msg []byte) error { return s.mirror.SendControl(serial, msg) },
		}
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			var ev controlEvent
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			_ = handleControlEvent(sink, &ev) // 控制失败不影响视频流
		}
	}()

	for p := range sub.Chan() {
		if p.Kind == scrcpy.PacketResize {
			if err := conn.WriteMessage(websocket.TextMessage, p.Data); err != nil {
				return
			}
			continue
		}
		if !sendPacket(conn, p) {
			return
		}
	}
	// 流已结束（设备断开或服务端停止）
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "stream ended"))
}

// sendPacket 把一帧按 [flags][pts][payload] 封帧写出。
func sendPacket(conn *websocket.Conn, p scrcpy.Packet) bool {
	buf := make([]byte, 9, 9+len(p.Data))
	var flags byte
	if p.Config {
		flags |= 1
	}
	if p.Key {
		flags |= 2
	}
	buf[0] = flags
	binary.BigEndian.PutUint64(buf[1:9], p.PTS)
	buf = append(buf, p.Data...)
	return conn.WriteMessage(websocket.BinaryMessage, buf) == nil
}
