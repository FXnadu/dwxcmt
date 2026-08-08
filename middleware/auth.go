package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"dwxcmt/pkg/utils"
)

type ctxKey string

// ClaimsKey 存放解析后的 JWT Claims 的上下文键
const ClaimsKey ctxKey = "claims"

// TokenBlacklist 内存 token 黑名单（登出后失效），重启后清空（依赖 JWT 自然过期）
type TokenBlacklist struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token -> 过期时间
	stop   chan struct{}
}

// NewTokenBlacklist 构造黑名单并启动定期清理
func NewTokenBlacklist() *TokenBlacklist {
	b := &TokenBlacklist{
		tokens: make(map[string]time.Time),
		stop:   make(chan struct{}),
	}
	go b.cleanup()
	return b
}

// Add 将 token 加入黑名单
func (b *TokenBlacklist) Add(token string, expiresAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens[token] = expiresAt
}

// Contains 检查 token 是否已被拉黑
func (b *TokenBlacklist) Contains(token string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	exp, ok := b.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(b.tokens, token)
		return false
	}
	return true
}

// Stop 停止后台清理协程
func (b *TokenBlacklist) Stop() {
	close(b.stop)
}

func (b *TokenBlacklist) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			now := time.Now()
			for t, exp := range b.tokens {
				if now.After(exp) {
					delete(b.tokens, t)
				}
			}
			b.mu.Unlock()
		case <-b.stop:
			return
		}
	}
}

// Auth JWT 鉴权中间件
type Auth struct {
	JWT       *utils.JWT
	Blacklist *TokenBlacklist
}

// Middleware 校验 Authorization: Bearer <token>
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if !strings.HasPrefix(authz, "Bearer ") {
			utils.Fail(w, utils.CodeErrTokenInvalid)
			return
		}
		tokenStr := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		if tokenStr == "" || a.Blacklist.Contains(tokenStr) {
			utils.Fail(w, utils.CodeErrTokenInvalid)
			return
		}
		claims, err := a.JWT.Parse(tokenStr)
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				utils.Fail(w, utils.CodeErrTokenExpired)
			} else {
				utils.Fail(w, utils.CodeErrTokenInvalid)
			}
			return
		}
		// 二次验证预授权凭证（purpose="2fa"）仅用于完成 2FA，不可直接访问受保护接口
		if claims.Purpose != "" {
			utils.Fail(w, utils.CodeErrTokenInvalid)
			return
		}
		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
