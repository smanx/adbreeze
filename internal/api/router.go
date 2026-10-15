// Package api 提供 REST 接口、WebSocket 推送与嵌入式静态前端。
package api

import (
	"io/fs"
	"log"
	"net/http"

	"adbreeze/internal/adb"
	"adbreeze/internal/auth"
	"adbreeze/internal/scrcpy"
	"adbreeze/internal/webrtc"
)

// Server 承载 HTTP 服务。
type Server struct {
	token   string
	version string
	mgr     *adb.Manager
	mirror  *scrcpy.Manager
	webrtc  *webrtc.Manager
}

// New 组装路由，返回可直接交给 http.Server 的 Handler。
func New(token, version string, mgr *adb.Manager, mirror *scrcpy.Manager, webFS fs.FS) http.Handler {
	s := &Server{token: token, version: version, mgr: mgr, mirror: mirror, webrtc: webrtc.NewManager()}
	mux := http.NewServeMux()

	// 健康检查（无需鉴权）
	mux.HandleFunc("GET /api/health", s.health)

	// REST API（需鉴权）
	authed := http.NewServeMux()
	authed.HandleFunc("GET /api/devices", s.listDevices)
	authed.HandleFunc("POST /api/devices/connect", s.connectDevice)
	authed.HandleFunc("DELETE /api/devices/{serial}", s.disconnectDevice)
	authed.HandleFunc("DELETE /api/devices/{serial}/remember", s.forgetDevice)
	authed.HandleFunc("POST /api/devices/{serial}/shell", s.shell)
	authed.HandleFunc("POST /api/devices/{serial}/mirror", s.startMirror)
	authed.HandleFunc("GET /api/devices/{serial}/mirror", s.mirrorInfo)
	authed.HandleFunc("DELETE /api/devices/{serial}/mirror", s.stopMirror)
	authed.HandleFunc("POST /api/devices/{serial}/webrtc", s.createWebRTC)
	authed.HandleFunc("GET /api/mirrors", s.listMirrors)
	authed.HandleFunc("GET /api/devices/{serial}/files", s.listFiles)
	authed.HandleFunc("PUT /api/devices/{serial}/files", s.uploadFile)
	authed.HandleFunc("GET /api/devices/{serial}/files/content", s.downloadFile)
	authed.HandleFunc("DELETE /api/devices/{serial}/files", s.deleteFile)
	authed.HandleFunc("POST /api/devices/{serial}/files/mkdir", s.mkdir)
	authed.HandleFunc("POST /api/devices/{serial}/files/rename", s.renameFile)
	authed.HandleFunc("GET /api/devices/{serial}/clipboard", s.getClipboard)
	authed.HandleFunc("POST /api/devices/{serial}/clipboard", s.setClipboard)
	authed.HandleFunc("GET /api/devices/{serial}/screenshot", s.screenshot)
	authed.HandleFunc("POST /api/devices/{serial}/install", s.installAPK)
	authed.HandleFunc("GET /api/devices/{serial}/overview", s.handleOverview)
	authed.HandleFunc("GET /api/devices/{serial}/perf", s.handlePerf)
	authed.HandleFunc("GET /api/devices/{serial}/ui-dump", s.handleUIDump)
	authed.HandleFunc("GET /api/devices/{serial}/apps", s.handleAppList)
	authed.HandleFunc("GET /api/devices/{serial}/apps/info", s.handleAppInfo)
	authed.HandleFunc("POST /api/devices/{serial}/apps/action", s.handleAppAction)
	authed.HandleFunc("GET /api/devices/{serial}/apps/apk", s.handleAppApk)
	authed.HandleFunc("GET /api/devices/{serial}/processes", s.handleProcList)
	authed.HandleFunc("POST /api/devices/{serial}/processes/kill", s.handleProcKill)
	mux.Handle("/api/", auth.Middleware(s.token, authed))

	// WebSocket（需鉴权，token 走 query 参数）
	events := http.NewServeMux()
	events.HandleFunc("GET /ws/events", s.handleEvents)
	events.HandleFunc("GET /ws/video/{serial}", s.handleVideo)
	events.HandleFunc("GET /ws/audio/{serial}", s.handleAudio)
	events.HandleFunc("GET /ws/term/{serial}", s.handleTerm)
	events.HandleFunc("GET /ws/logcat/{serial}", s.handleLogcat)
	mux.Handle("/ws/", auth.Middleware(s.token, events))

	// 静态前端（embed 进二进制）
	mux.Handle("/", http.FileServerFS(webFS))

	return logRequest(mux)
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
