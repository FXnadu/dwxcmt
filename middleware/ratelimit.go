package middleware

import (
	"sync"
	"time"
)

// Limiter 固定窗口限流器（线程安全），用于接口级限流
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*windowBucket
	stop   chan struct{}
}

type windowBucket struct {
	count     int
	windowEnd time.Time
}

// NewLimiter 构造限流器，limit 为窗口内最大次数；startCleanup=true 时启动定期清理
func NewLimiter(limit int, window time.Duration, startCleanup bool) *Limiter {
	l := &Limiter{
		limit:  limit,
		window: window,
		hits:   make(map[string]*windowBucket),
		stop:   make(chan struct{}),
	}
	if startCleanup {
		go l.cleanup()
	}
	return l
}

// Allow 判断 key 是否允许通过；通过则计数 +1
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.hits[key]
	if !ok || now.After(w.windowEnd) {
		l.hits[key] = &windowBucket{count: 1, windowEnd: now.Add(l.window)}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// Stop 停止后台清理协程
func (l *Limiter) Stop() {
	close(l.stop)
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for k, w := range l.hits {
				if now.After(w.windowEnd) {
					delete(l.hits, k)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}
