// Package auth 提供管理员密码登录与会话 cookie/token 机制。
//
// 页面与 API 由前端登录表单完成鉴权：用户提交管理员密码到 POST /api/session，
// 服务端校验通过后签发短期会话（HttpOnly cookie + 响应体回传 token）。
// 之后所有请求凭会话放行——cookie（同源）或 Authorization: Bearer（跨源 fetch）、
// ?session= 查询参数（跨源 WebSocket，浏览器既不随跨站发 cookie 也发不了自定义头）。
// 相比 Basic Auth：密码只在登录时提交一次、登出即真失效、不再受浏览器凭证缓存摆布。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "ADBreeze-Session"
	sessionTTL        = 12 * time.Hour

	// 登录防爆破：连续失败 maxFails 次后锁定 lockout 时长（成功登录即清零）。
	maxFails = 5
	lockout  = 30 * time.Second
)

// Authenticator 持有管理员密码，并负责签发/校验会话 cookie。
type Authenticator struct {
	password string
	key      []byte // cookie 签名密钥，每次启动随机生成，进程重启后旧会话失效

	mu          sync.Mutex
	fails       int       // 连续登录失败次数
	lockedUntil time.Time // 失败过多后的锁定截止时刻
}

// New 构造认证器。
func New(password string) *Authenticator {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("生成会话签名密钥失败: " + err.Error())
	}
	return &Authenticator{password: password, key: key}
}

// Middleware 保护普通 HTTP：会话有效（cookie / Bearer / ?session=）则放行，
// 否则返回 401 JSON，由前端展示登录表单。
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		unauthorized(w)
	})
}

// WSMiddleware 保护 WebSocket：握手只能凭会话放行
// （WS 发不了自定义头，缺失/过期时返回 401，由前端重走 session 换发）。
// 接受会话 cookie 或 ?session=<token> 查询参数：跨源前端拿不到也发不了
// 这个 cookie（SameSite=Lax 不随跨站请求发送），由 Login 在响应体里
// 回传同一 token 经 URL 传递。
func (a *Authenticator) WSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.validToken(r.URL.Query().Get("session")) || a.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "session expired", http.StatusUnauthorized)
	})
}

// Login 处理登录：已持有效会话则直接续签；否则校验请求体里的管理员密码
// {"password": "..."}，通过后签发会话 cookie，并把 token 写进响应体
// （跨源前端的 cookie 不可用，需要它经 URL 查询参数完成 WS 鉴权、
// 经 Authorization 头完成 REST 调用）。
// 连续失败 maxFails 次进入 lockout 锁定期，锁定期内一律 429，不再校验密码。
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	if a.authenticated(r) {
		a.issue(w)
		return
	}
	if !a.allowAttempt() {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", strconv.Itoa(int(lockout.Seconds())))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "too many failed attempts"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		unauthorized(w)
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(a.password)) != 1 {
		a.recordFail()
		unauthorized(w)
		return
	}
	a.recordSuccess()
	a.issue(w)
}

// issue 签发会话：写 cookie + 响应体回传 token。
func (a *Authenticator) issue(w http.ResponseWriter) {
	expiry := time.Now().Add(sessionTTL)
	token := a.sign(expiry)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiry,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"session": token})
}

// ClearSession 清除会话 cookie。登录态只有这一处凭证（cookie / 前端保存的
// token，后者由前端同步删除），服务端签发密钥重启即换——退出即真退出。
func (a *Authenticator) ClearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// allowAttempt 报告当前是否允许尝试登录（防爆破锁定判断）。
func (a *Authenticator) allowAttempt() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().After(a.lockedUntil)
}

func (a *Authenticator) recordFail() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fails++
	if a.fails >= maxFails {
		a.lockedUntil = time.Now().Add(lockout)
		a.fails = 0
	}
}

func (a *Authenticator) recordSuccess() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fails = 0
	a.lockedUntil = time.Time{}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}

// authenticated 依次检查三种会话凭证：
//   - 会话 cookie（同源请求由浏览器自动携带）
//   - Authorization: Bearer <token>（跨源 fetch：cookie 不随跨站发送）
//   - ?session=<token> 查询参数（兜底，主要为跨源 WebSocket 服务）
func (a *Authenticator) authenticated(r *http.Request) bool {
	if a.validSession(r) {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if a.validToken(strings.TrimPrefix(h, "Bearer ")) {
			return true
		}
	}
	return a.validToken(r.URL.Query().Get("session"))
}

// sign 格式：<过期 unix 秒>.<base64url(HMAC-SHA256(key, unix 秒))>
func (a *Authenticator) sign(expiry time.Time) string {
	exp := strconv.FormatInt(expiry.Unix(), 10)
	return exp + "." + a.mac(exp)
}

func (a *Authenticator) mac(payload string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Authenticator) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return a.validToken(c.Value)
}

// validToken 校验裸 token（cookie 值 / Bearer / ?session= 查询参数）。
// 格式：<过期 unix 秒>.<base64url(HMAC-SHA256(key, unix 秒))>
func (a *Authenticator) validToken(token string) bool {
	exp, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(a.mac(exp))) {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < n
}
