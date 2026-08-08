package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // 注册 /debug/pprof/*（仅 127.0.0.1:6060 观测端点暴露）
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dwxcmt/config"
	"dwxcmt/controller"
	"dwxcmt/middleware"
	"dwxcmt/model"
	"dwxcmt/pkg/cache"
	"dwxcmt/pkg/email"
	"dwxcmt/pkg/utils"
	"dwxcmt/router"
	"dwxcmt/service"
)

// allLoopback 判断列表是否为空或全部为回环地址（用于上线前配置自检）
func allLoopback(list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, s := range list {
		s = strings.TrimSpace(s)
		switch s {
		case "", "127.0.0.1", "::1", "localhost", "127.0.0.1/32", "::1/128":
			continue
		}
		return false
	}
	return true
}

func main() {
	configPath := flag.String("config", "config/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	// 安全校验：默认 JWT 密钥必须替换（debug 模式允许但强烈警告，release 模式强制退出）
	if err := cfg.Validate(); err != nil {
		if cfg.Server.Mode == "debug" {
			log.Printf("[警告] %v（debug 模式允许继续，release 模式将强制退出）", err)
		} else {
			log.Fatalf("[致命] %v", err)
		}
	}

	// 注入可信反代列表；失败不启动（CIDR 非法属于配置错误）
	if err := utils.SetTrustedProxies(cfg.TrustedProxy.Proxies); err != nil {
		log.Fatalf("解析 trusted_proxy.proxies 失败: %v", err)
	}
	log.Printf("可信反代列表（%d 条）: %v", len(cfg.TrustedProxy.Proxies), cfg.TrustedProxy.Proxies)

	// 上线前配置自检：release 模式且仅在回环白名单时打印警告，防止误部署导致
	// 前置反代/CDN 时所有用户共享同一 IP 限流桶，或跨域嵌入被浏览器拦截。
	if cfg.Server.Mode != "debug" && allLoopback(cfg.TrustedProxy.Proxies) {
		log.Printf("[警告] trusted_proxy 仅信任回环地址；若前置 Nginx/CDN，请在 config.yaml 的 trusted_proxy.proxies 中追加其出口 IP/CIDR，否则所有用户将共享同一 IP 的限流配额")
	}
	if cfg.Server.Mode != "debug" && allLoopback(cfg.CORS.AllowedOrigins) {
		log.Printf("[警告] cors.allowed_origins 仅含回环地址；若评论组件嵌入其他域名页面，请将对应域名加入白名单，否则浏览器将拦截跨域请求")
	}

	db, err := model.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer db.Close()

	c := cache.New(512, 60*time.Second)
	svc := service.New(db, cfg, c)

	blacklist := middleware.NewTokenBlacklist()
	defer blacklist.Stop()
	auth := &middleware.Auth{JWT: svc.JWT, Blacklist: blacklist}

	globalLimiter := middleware.NewLimiter(cfg.RateLimit.RequestsPerSecond, time.Second, true)
	loginLimiter := middleware.NewLimiter(5, time.Minute, true)
	defer globalLimiter.Stop()
	defer loginLimiter.Stop()

	// T4 邮件通知：svc 实现 email.SettingsLoader，SMTP 未配置时静默跳过
	svc.Notifier = email.NewNotifier(cfg.SMTP, svc)

	// 定时清理过期数据（点赞去重记录 1 分钟 / 每日限流记录 30 天），防止表无限增长。
	// 清理不影响 like_count 计数与当前限流窗口。
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := svc.CleanupStaleData(time.Now()); err != nil {
				log.Printf("[cleanup] 清理过期数据失败: %v", err)
			}
		}
	}()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router.New(cfg, svc, auth, globalLimiter, loginLimiter),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 性能观测端点：仅绑定本机回环，供 monitor.sh / go tool pprof 使用，外网不可达。
	// 端口固定 6060（http.DefaultServeMux 未被业务路由占用，可安全复用）。
	go func() {
		pprofSrv := &http.Server{
			Addr:              "127.0.0.1:6060",
			Handler:           http.DefaultServeMux, // net/http/pprof 在此注册 /debug/pprof/*
			ReadHeaderTimeout: 5 * time.Second,
		}
		if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[pprof] 观测端点启动失败: %v", err)
		}
	}()

	// 优雅退出：监听信号，最长等待 30 秒（决策 #20）
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("收到退出信号，等待任务完成（最长 30 秒）...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("优雅退出失败: %v", err)
		}
	}()

	log.Printf("dwxComment v%s 启动，监听 :%d", controller.Version, cfg.Server.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务启动失败: %v", err)
	}
	log.Println("服务已退出")
}
