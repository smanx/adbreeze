package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
)

// termEvent 是终端 WS 的上行消息。
type termEvent struct {
	Type string `json:"type"` // input / exit
	Data string `json:"data,omitempty"`
}

// handleTerm 提供交互式终端（adb PTY shell）。
//
// 服务端 → 客户端：二进制帧 = PTY 原始输出（含 ANSI 转义序列，xterm.js 直接渲染）。
// 客户端 → 服务端：JSON {"type":"input","data":"..."} / {"type":"exit"}。
func (s *Server) handleTerm(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	// 设备在线预检，给出明确错误
	online := false
	for _, d := range s.mgr.Devices() {
		if d.Serial == serial && d.State == "device" {
			online = true
			break
		}
	}
	if !online {
		http.Error(w, "设备不在线或未授权", http.StatusConflict)
		return
	}

	pty, err := s.mgr.Client().ShellPtyConn(serial, "")
	if err != nil {
		http.Error(w, "打开终端失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer pty.Close()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// PTY → WS
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8*1024)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "terminal closed"))
				return
			}
		}
	}()

	// WS → PTY
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var ev termEvent
		if json.Unmarshal(data, &ev) != nil || ev.Type == "" {
			continue
		}
		switch ev.Type {
		case "input":
			if _, err := pty.Write([]byte(ev.Data)); err != nil {
				return
			}
		case "exit":
			return
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
