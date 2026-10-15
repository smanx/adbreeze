// Package auth 提供 HTTP Basic Auth 与 WebSocket 会话 cookie 机制。
//
// 页面与 API 请求由浏览器自带的 Basic 弹窗直接校验账号密码；
// WebSocket 握手既无法触发 Basic 弹窗也不便携带 Authorization 头，
// 因此前端在建立 WS 前用 Basic 凭证换取短期会话 cookie（POST /api/session），
// WS 请求凭该 cookie 通过校验。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "ADBreeze-Session"
	sessionTTL        = 12 * time.Hour
)

// Authenticator 持有 Basic 凭证，并负责签发/校验会话 cookie。
type Authenticator struct {
	username string
	password string
	key      []byte // cookie 签名密钥，每次启动随机生成，进程重启后旧会话失效
}

// New 构造认证器。
func New(username, password string) *Authenticator {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("生成会话签名密钥失败: " + err.Error())
	}
	return &Authenticator{username: username, password: password, key: key}
}

// Middleware 保护普通 HTTP：会话 cookie 有效或 Basic 凭证正确则放行，
// 否则返回 401 触发浏览器自带登录弹窗。
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		a.challenge(w)
	})
}

// WSMiddleware 保护 WebSocket：握手只能凭会话 cookie 放行
// （WS 无法触发 Basic 弹窗，缺失/过期时返回 401，由前端重走 session 换发）。
func (a *Authenticator) WSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "session expired", http.StatusUnauthorized)
	})
}

// IssueSession 校验 Basic 凭证后签发会话 cookie，供前端建立 WebSocket 使用。
func (a *Authenticator) IssueSession(w http.ResponseWriter, r *http.Request) {
	if !a.authenticated(r) {
		a.challenge(w)
		return
	}
	expiry := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    a.sign(expiry),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiry,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

// ClearSession 清除会话 cookie。浏览器缓存的 Basic 凭证无法由 JS 清除，
// 彻底退出需要用户关闭浏览器标签页 / 清除站点凭据。
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

func (a *Authenticator) authenticated(r *http.Request) bool {
	if a.validSession(r) {
		return true
	}
	u, p, ok := r.BasicAuth()
	return ok && a.checkBasic(u, p)
}

func (a *Authenticator) checkBasic(u, p string) bool {
	return subtle.ConstantTimeCompare([]byte(u), []byte(a.username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(a.password)) == 1
}

func (a *Authenticator) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="ADBreeze", charset="UTF-8"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	exp, sig, ok := strings.Cut(c.Value, ".")
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
