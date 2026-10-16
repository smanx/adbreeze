package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"adbreeze/internal/scrcpy"
)

// getClipboard GET /api/devices/{serial}/clipboard
// 向设备请求剪贴板并等待上报（超时返回最近一次内容）。
func (s *Server) getClipboard(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	text, err := s.mirror.WaitClipboard(serial, 800*time.Millisecond)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

type setClipboardReq struct {
	Text  string `json:"text"`
	Paste bool   `json:"paste"` // true = 同时粘贴到设备当前输入框
}

// setClipboard POST /api/devices/{serial}/clipboard {"text":"...","paste":true}
func (s *Server) setClipboard(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	var req setClipboardReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeErr(w, http.StatusBadRequest, "需要 text 字段")
		return
	}
	msg, err := scrcpy.EncodeSetClipboard(req.Text, req.Paste)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.mirror.SendControl(serial, msg); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已写入剪贴板"})
}

// screenshot GET /api/devices/{serial}/screenshot
// screencap 到临时文件后经 sync 拉取，返回 PNG。
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	const remote = "/data/local/tmp/.adbreeze-shot.png"
	if _, err := s.mgr.Client().ShellTimeout(serial, "screencap -p "+remote, 20*time.Second); err != nil {
		writeErr(w, http.StatusBadGateway, "截图失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "attachment; filename="+serial+"-"+time.Now().Format("20060102-150405")+".png")
	if err := s.mgr.Client().Pull(serial, remote, w); err != nil {
		log.Printf("[screenshot:%s] 拉取失败: %v", serial, err)
	}
	_, _ = s.mgr.Client().ShellTimeout(serial, "rm -f "+remote, 5*time.Second)
}

// installAPK POST /api/devices/{serial}/install（body = APK 文件内容）
// 流程：push 到 /data/local/tmp → pm install -r → 清理。
func (s *Server) installAPK(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<30)) // APK 上限 4GB
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "请求体需要 APK 文件内容")
		return
	}
	const remote = "/data/local/tmp/.adbreeze-install.apk"
	if err := s.mgr.Client().Push(serial, remote, data, "0644"); err != nil {
		writeErr(w, http.StatusBadGateway, "推送 APK 失败: "+err.Error())
		return
	}
	out, err := s.mgr.Client().ShellTimeout(serial, "pm install -r "+remote, 180*time.Second)
	_, _ = s.mgr.Client().ShellTimeout(serial, "rm -f "+remote, 5*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "安装失败: "+err.Error()+": "+out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "安装完成", "output": out})
}

// installAPKFromPath POST /api/devices/{serial}/install/path {"path":"/sdcard/Download/x.apk"}
// APK 已在设备文件系统上（文件管理器里的安装按钮），直接 pm install -r，不经本机中转。
func (s *Server) installAPKFromPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !readJSONBody(w, r, &req) {
		return
	}
	p, ok := sanitizeDevicePath(req.Path)
	if !ok {
		writeErr(w, http.StatusBadRequest, "需要合法的 path 参数")
		return
	}
	if !strings.HasSuffix(strings.ToLower(p), ".apk") {
		writeErr(w, http.StatusBadRequest, "仅支持安装 APK 文件")
		return
	}
	serial := r.PathValue("serial")
	// 大 APK 安装可能超过一分钟，超时放宽到 300s
	out, err := s.mgr.Client().ShellTimeout(serial, "pm install -r '"+shQuote(p)+"'", 300*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "安装失败: "+err.Error()+": "+out)
		return
	}
	// 个别设备 shell 退出码不可靠（pm 失败仍返回 0），以输出里的 Success 为准
	if !strings.Contains(out, "Success") {
		writeErr(w, http.StatusBadGateway, "安装失败: "+strings.TrimSpace(out))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "安装完成", "output": out})
}
