package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// handleLogcat 推送设备实时日志（adb shell logcat 长驻连接）。
//
// 服务端 → 客户端：每条日志一行纯文本（TextMessage）。
// 客户端 → 服务端：JSON 控制消息 {"type":"clear"}（清屏：重启 logcat 丢弃旧缓冲）。
//
// 关闭码：4000 = logcat 断开（设备离线等），前端提示。
func (s *Server) handleLogcat(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	// 设备在线预检
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

	// -T 300 回填最近 300 行缓冲；logcat 进程随连接关闭自动终止
	logcat, err := s.mgr.Client().ShellConn(serial, "logcat -v threadtime -T 300 2>&1")
	if err != nil {
		http.Error(w, "打开 logcat 失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer logcat.Close()

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// 读协程：客户端控制消息
	ctrl := make(chan string, 4)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &msg) != nil || msg.Type != "clear" {
				continue
			}
			select {
			case ctrl <- "clear":
			default:
			}
		}
	}()

	lw := &lineWriter{ws: ws}
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-readDone:
			return
		case t := <-ctrl:
			if t == "clear" {
				// 重启 logcat（不带 -T 即从当前时间开始），实现清屏
				logcat.Close()
				newConn, err := s.mgr.Client().ShellConn(serial, "logcat -v threadtime 2>&1")
				if err != nil {
					_ = ws.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(4000, "重启 logcat 失败: "+err.Error()),
						time.Now().Add(time.Second))
					return
				}
				logcat = newConn
				defer logcat.Close()
				lw.reset()
			}
			continue
		default:
		}
		_ = logcat.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := logcat.Read(buf)
		if n > 0 {
			ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if werr := lw.write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue // 读超时是常态：回去收控制消息
			}
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(4000, "logcat 已断开"),
				time.Now().Add(time.Second))
			return
		}
	}
}

// lineWriter 把字节流按行切分后逐行发 WS（半行缓存到下一轮）。
type lineWriter struct {
	ws      *websocket.Conn
	pending []byte
}

func (w *lineWriter) reset() { w.pending = w.pending[:0] }

func (w *lineWriter) write(data []byte) error {
	w.pending = append(w.pending, data...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			// 半行太长防炸内存
			if len(w.pending) > 256*1024 {
				w.pending = w.pending[:0]
			}
			return nil
		}
		line := w.pending[:idx]
		w.pending = w.pending[idx+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			continue
		}
		if err := w.ws.WriteMessage(websocket.TextMessage, line); err != nil {
			return err
		}
	}
}
