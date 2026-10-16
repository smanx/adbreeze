// Package api 提供 REST 接口、WebSocket 推送与嵌入式静态前端。
package api

import (
	"io/fs"
	"log"
	"mime"
	"net/http"
	"strings"

	"adbreeze/internal/adb"
	"adbreeze/internal/auth"
	"adbreeze/internal/scrcpy"
	"adbreeze/internal/settings"
	"adbreeze/internal/tlsutil"
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
	tls      tlsutil.Info // HTTPS 证书元信息（设置页证书安装功能用）
}

// New 组装路由，返回可直接交给 http.Server 的 Handler。
// corsOrigins 为跨域放行来源（逗号分隔，空 = 不限制），见 cors。
// tlsInfo 为当前证书元信息（见 tlsutil.Describe），供证书下载端点使用。
func New(a *auth.Authenticator, version string, mgr *adb.Manager, mirror *scrcpy.Manager, webFS fs.FS, st *settings.Store, corsOrigins string, tlsInfo tlsutil.Info) http.Handler {
	s := &Server{auth: a, version: version, mgr: mgr, mirror: mirror, webrtc: webrtc.NewManager(), settings: st, tls: tlsInfo}
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
	// HTTPS 证书：状态查询 + CA 证书 / Apple 描述文件下载（设置页安装引导）
	authed.HandleFunc("GET /api/settings/tls", s.tlsStatus)
	authed.HandleFunc("GET /api/settings/tls/ca.crt", s.downloadCACert)
	authed.HandleFunc("GET /api/settings/tls/ca.mobileconfig", s.downloadMobileConfig)
	mux.Handle("/api/", a.Middleware(authed))

	// WebSocket（需鉴权，凭 /api/session 签发的会话 cookie）
	events := http.NewServeMux()
	events.HandleFunc("GET /ws/events", s.handleEvents)
	events.HandleFunc("GET /ws/video/{serial}", s.handleVideo)
	events.HandleFunc("GET /ws/audio/{serial}", s.handleAudio)
	events.HandleFunc("GET /ws/term/{serial}", s.handleTerm)
	events.HandleFunc("GET /ws/logcat/{serial}", s.handleLogcat)
	mux.Handle("/ws/", a.WSMiddleware(events))

	// 静态前端（embed 进二进制）。
	// PWA 外壳资源必须免鉴权：浏览器抓取 manifest 时默认不带 Basic 凭证
	// （同源也不例外——需要凭证的 manifest 得显式声明 crossorigin="use-credentials"），
	// 否则一律 401，Chrome 里表现为「manifest 加载失败 / 无法安装 PWA」；图标同理。
	// 这些是品牌性静态资源，不含敏感信息，公开无妨；应用本体与全部 API / WS 仍受保护。
	web := http.FileServerFS(webFS)
	mux.Handle("GET /manifest.webmanifest", web)
	mux.Handle("GET /sw.js", web)
	mux.Handle("GET /icons/", web)
	mux.Handle("/", a.Middleware(web))

	return logRequest(cors(mux, parseOrigins(corsOrigins)))
}

// cors 为跨源前端放行 API：前端可托管在另一域名（静态 Pages、反代等），
// 页面里手动配置后端地址后由浏览器直接访问本服务。
//
// 携带凭证的请求（Authorization 头 / cookie）有两条硬约束：
//   - Access-Control-Allow-Origin 必须回显具体 Origin，不能是 *；
//   - 必须同时带 Access-Control-Allow-Credentials: true。
// 预检（OPTIONS）在鉴权之前应答：凭证头不属于简单请求，浏览器会先发预检。
// allowed 为空表示不限制来源；非空时仅放行名单内的 Origin。
func cors(next http.Handler, allowed map[string]struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r) // 同源请求无 Origin 头，与既往行为一致
			return
		}
		if len(allowed) > 0 {
			if _, ok := allowed[origin]; !ok {
				next.ServeHTTP(w, r) // 不在名单：不加 CORS 头，由浏览器拦截
				return
			}
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		h.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// parseOrigins 把逗号分隔的来源清单解析成集合（去空白、忽略空项）。
func parseOrigins(csv string) map[string]struct{} {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, o := range strings.Split(csv, ",") {
		if o = strings.TrimSpace(o); o != "" {
			set[o] = struct{}{}
		}
	}
	return set
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
