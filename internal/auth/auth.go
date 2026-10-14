// Package auth 提供 API 鉴权中间件。
package auth

import (
	"crypto/subtle"
	"net/http"
)

// Middleware 校验 token：优先 Header X-Auth-Token，其次 query 参数 token（供 WebSocket 使用）。
// 均不匹配时返回 401。
func Middleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Auth-Token")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
