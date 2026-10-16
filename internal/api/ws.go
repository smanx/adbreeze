package api

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sync"
	"time"

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
// mode=ctrl 时为纯控制通道（WebRTC 方案使用）：只交换 meta/resize 与控制事件，不推帧。
//
// 服务端 → 客户端：
//   - 第一条 JSON 文本消息: {"type":"meta","serial":...,"device":...,"codec":"h264","width":...,"height":...}
//   - 二进制帧: [1B flags( bit0=config, bit1=key )][8B PTS u64 BE][8B RecvWall u64 BE][8B SendWall u64 BE][H.264 AnnexB 载荷]
//     RecvWall 是后端读到该帧的墙钟（Unix 微秒），SendWall 是 WS 写出的墙钟：
//     前端据此把「后端→浏览器」拆成后端扇出（读→写，含 hub 队列）与网络+派发两段。
//     设备 PTS 是设备编码器的单调时钟，与墙钟不同源，故延迟只能分段测。
//   - 时钟同步: {"type":"pong","t0":<回显>,"t1":<后端收到>,"t2":<后端发出>}（墙钟毫秒，
//     回应客户端的 {"type":"ping","t0":...}）——传输段要跨双端墙钟相减，必须先校准偏移
//   - 尺寸变化时发送 JSON 文本消息: {"type":"resize","width":..,"height":..}
//
// 客户端 → 服务端：JSON 控制事件（touch/key/text/scroll/op，见 control.go）。
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("mode") == "ctrl" {
		s.handleVideoCtrl(w, r)
		return
	}
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

	// 写互斥：帧写出（主循环）与 pong 回复（读协程）共用同一 conn，
	// gorilla 要求写操作串行
	var writeMu sync.Mutex
	sendFrame := func(p scrcpy.Packet) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		return sendVideoPacket(conn, p)
	}
	writeJSON := func(v any) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v) == nil
	}

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
		if !sendFrame(scrcpy.Packet{Kind: scrcpy.PacketVideo, Config: true, Data: cachedConfig}) {
			return
		}
	}

	// 读协程：控制事件（JSON）→ ControlMessage → 设备；
	// ping 单独应答 pong（NTP 式时钟同步，见文件头注释）
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
			if ev.Type == "ping" {
				t1 := time.Now().UnixMilli()
				t2 := time.Now().UnixMilli()
				writeJSON(map[string]any{"type": "pong", "t0": ev.T0, "t1": t1, "t2": t2})
				continue
			}
			_ = handleControlEvent(sink, &ev) // 控制失败不影响视频流
		}
	}()

	for p := range sub.Chan() {
		if p.Kind == scrcpy.PacketResize {
			writeMu.Lock()
			werr := conn.WriteMessage(websocket.TextMessage, p.Data)
			writeMu.Unlock()
			if werr != nil {
				return
			}
			continue
		}
		if !sendFrame(p) {
			return
		}
	}
	// 流已结束（设备断开或服务端停止）
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "stream ended"))
}

// handleVideoCtrl 是 WebRTC 方案使用的纯控制通道：交换 meta 与控制事件，
// 周期检查流存活，流结束后主动关闭通知前端。
func (s *Server) handleVideoCtrl(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	info, err := s.mirror.Info(serial)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
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

	// 读协程：控制事件（与帧推模式相同的处理）
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
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
				return
			}
			var ev controlEvent
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			_ = handleControlEvent(sink, &ev)
		}
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := s.mirror.Info(serial); err != nil { // 流已结束
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "stream ended"))
				return
			}
		}
	}
}

// handleAudio 推送设备音频流（Opus，帧格式与视频一致：[flags][pts][payload]）。
// 首条 JSON 文本消息: {"type":"meta","codec":"opus"}；配置包为 OpusHead。
func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	sub, cancel, cachedConfig, err := s.mirror.SubscribeAudio(serial)
	if err != nil && !s.mirror.AudioDisabled(serial) {
		// 音频尚未就绪（采集初始化慢），客户端会以 300ms 重试
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	audioDisabled := err != nil // 设备端已明确禁用音频：升级后以 4003 关闭，客户端立即停止重试

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if !audioDisabled {
			cancel()
		}
		return
	}
	defer conn.Close()

	if audioDisabled {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4003, "设备端音频不可用"),
			time.Now().Add(time.Second))
		return
	}
	defer cancel()

	if err := conn.WriteJSON(map[string]any{"type": "meta", "codec": "opus"}); err != nil {
		return
	}
	if cachedConfig != nil {
		if !sendPacket(conn, scrcpy.Packet{Kind: scrcpy.PacketVideo, Config: true, Data: cachedConfig}) {
			return
		}
	}

	// 读协程：检测客户端主动断开
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	for p := range sub.Chan() {
		if !sendPacket(conn, p) {
			return
		}
	}
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseGoingAway, "audio ended"))
}

// sendPacket 把一帧按 [flags][pts][payload] 封帧写出（音频通道，9B 头）。
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

// sendVideoPacket 把一帧按 [flags][pts u64 BE][recvWall u64 BE][sendWall u64 BE][payload]
// 封帧写出（视频通道，25B 头）。比音频多 16B 两个后端墙钟：
//   - recvWall：从设备隧道读到该帧的时刻（调用方经 Packet.RecvWall 传入，0 则取当前）
//   - sendWall：WS 写出时刻（本函数打戳）
// 前端据此把「后端→浏览器」拆成 后端扇出（recvWall→sendWall，含 hub 队列）与
// 网络+派发（sendWall→浏览器收到）两段；两戳同为后端时钟，相减不受双端时钟差影响，
// 只有网络段需要先做 ping/pong 时钟校准。设备 PTS 为设备编码器的单调时钟，与墙钟
// 不可比，只能分段测，见 scrcpy.Packet.RecvWall。
func sendVideoPacket(conn *websocket.Conn, p scrcpy.Packet) bool {
	recvWall := p.RecvWall
	if recvWall == 0 {
		recvWall = time.Now().UnixMicro()
	}
	sendWall := time.Now().UnixMicro()
	var flags byte
	if p.Config {
		flags |= 1
	}
	if p.Key {
		flags |= 2
	}
	buf := make([]byte, 25, 25+len(p.Data))
	buf[0] = flags
	binary.BigEndian.PutUint64(buf[1:9], p.PTS)
	binary.BigEndian.PutUint64(buf[9:17], uint64(recvWall))
	binary.BigEndian.PutUint64(buf[17:25], uint64(sendWall))
	buf = append(buf, p.Data...)
	return conn.WriteMessage(websocket.BinaryMessage, buf) == nil
}

// encodeVideoFrame 封一帧：[1B flags][8B PTS BE][8B recvWall BE][8B sendWall BE][payload]。
// 与前端 controller.ts 的解析（偏移 0/1/9/17，载荷自 25 起）一一对应，改动需同步。
func encodeVideoFrame(flags byte, pts, recvWall, sendWall uint64, payload []byte) []byte {
	buf := make([]byte, 25, 25+len(payload))
	buf[0] = flags
	binary.BigEndian.PutUint64(buf[1:9], pts)
	binary.BigEndian.PutUint64(buf[9:17], recvWall)
	binary.BigEndian.PutUint64(buf[17:25], sendWall)
	return append(buf, payload...)
}
