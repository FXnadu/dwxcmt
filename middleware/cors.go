package middleware

import (
	"net/http"
	"net/url"
)

// CORS 跨域中间件：生产来源按白名单精确匹配，localhost/127.0.0.1 本地开发来源放行任意端口
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && isAllowedOrigin(origin, allowed) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isAllowedOrigin 判断 Origin 是否被允许：精确命中白名单，或为 localhost/127.0.0.1 的本地开发来源（任意端口）
func isAllowedOrigin(origin string, allowed map[string]bool) bool {
	if allowed[origin] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return (host == "localhost" || host == "127.0.0.1") && (u.Scheme == "http" || u.Scheme == "https")
}
