package middleware

import (
	"log"
	"net/http"
	"runtime/debug"

	"dwxcmt/pkg/utils"
)

// Recovery 捕获 panic，返回统一 500 响应，避免进程崩溃
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[panic] %v\n%s", rec, debug.Stack())
				utils.Error(w, http.StatusInternalServerError, utils.CodeErrInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
