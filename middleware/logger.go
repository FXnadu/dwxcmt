package middleware

import (
	"log"
	"net/http"
	"time"

	"dwxcmt/pkg/utils"
)

// Logger 请求日志：debug 模式记录全部，release 模式仅记录非 2xx
func Logger(debug bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(lw, r)
			if debug || lw.status >= 400 {
				log.Printf("%s %s %d %s ip=%s", r.Method, r.URL.RequestURI(), lw.status, time.Since(start).Round(time.Microsecond), utils.ClientIP(r))
			}
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
