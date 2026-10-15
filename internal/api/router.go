// Package api 提供 REST 接口、WebSocket 推送与嵌入式静态前端。
package api

import (
	"io/fs"
	"log"
	"mime"
	"net/http"

	"adbreeze/internal/adb"
	"adbreeze/internal/auth"
	"adbreeze/internal/scrcpy"
	"adbreeze/internal/settings"
	"adbreeze/internal/webrtc"
)

func init() {
	// Go 的 mime 表不认识 .webmanifest，默认按 application/octet-stream 下发，
	// 浏览器会因 MIME 不合法而拒绝解析 PWA manifest（表现为"无法安装"）。
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Server 承载 HTTP 服务。
type Server struct {
	auth     *auth.Authenticator
	version  string
	mgr      *adb.Manager
	mirror   *scrcpy.Manager
	webrtc   *webrtc.Manager
	settings *settings.Store
}

// New 组装路由，返回可直接交给 http.Server 的 Handler。
func New(a *auth.Authenticator, version string, mgr *adb.Manager, mirror *scrcpy.Manager, webFS fs.FS, st *settings.Store) http.Handler {
	s := &Server{auth: a, version: version, mgr: mgr, mirror: mirror, webrtc: webrtc.NewManager(), settings: st}
	mux := http.NewServeMux()

	// 健康检查（无需鉴权）
	mux.HandleFunc("GET /api/health", s.health)
	// WebSocket 会话 cookie 签发/清除（走 Basic 校验，浏览器自动带凭证）
	mux.HandleFunc("POST /api/session", a.IssueSession)
	mux.HandleFunc("DELETE /api/session", a.ClearSession)

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
	// 运行时设置（adb 可执行文件路径可随时切换）
	authed.HandleFunc("GET /api/settings/adb", s.getAdbSettings)
	authed.HandleFunc("POST /api/settings/adb", s.putAdbSettings)
	mux.Handle("/api/", a.Middleware(authed))

	// WebSocket（需鉴权，凭 /api/session 签发的会话 cookie）
	events := http.NewServeMux()
	events.HandleFunc("GET /ws/events", s.handleEvents)
	events.HandleFunc("GET /ws/video/{serial}", s.handleVideo)
	events.HandleFunc("GET /ws/audio/{serial}", s.handleAudio)
	events.HandleFunc("GET /ws/term/{serial}", s.handleTerm)
	events.HandleFunc("GET /ws/logcat/{serial}", s.handleLogcat)
	mux.Handle("/ws/", a.WSMiddleware(events))

	// 静态前端（embed 进二进制）——同样受保护，浏览器在加载页面即弹出登录框
	mux.Handle("/", a.Middleware(http.FileServerFS(webFS)))

	return logRequest(mux)
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
