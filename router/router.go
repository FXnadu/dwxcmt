package router

import (
	"net/http"

	"dwxcmt/config"
	"dwxcmt/controller"
	"dwxcmt/middleware"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// New 组装完整 HTTP 路由（含中间件链）
func New(cfg *config.Config, svc *service.Service, auth *middleware.Auth, globalLimiter, loginLimiter *middleware.Limiter) http.Handler {
	commentCtl := controller.NewComment(svc)
	adminCtl := controller.NewAdmin(svc, auth)

	api := http.NewServeMux()

	// ===== 公开接口 =====
	api.HandleFunc("GET /api/v1/health", commentCtl.Health)
	api.HandleFunc("GET /api/v1/comments", commentCtl.List)
	api.HandleFunc("GET /api/v1/comments/count", commentCtl.Count)
	api.HandleFunc("GET /api/v1/site-config", commentCtl.SiteConfig)
	api.HandleFunc("POST /api/v1/comment", commentCtl.Submit)
	api.HandleFunc("POST /api/v1/comment/{id}/like", commentCtl.Like)
	// QQ 头像代理（脱敏：公开响应不含 QQ 号，由服务端代拉图片）
	api.HandleFunc("GET /api/v1/avatars/{id}", commentCtl.Avatar)

	// ===== 管理接口 =====
	// 注册 / 登录不要求 JWT，但均加严格限流（bcrypt cost=12 计算开销大，防 CPU 耗尽 DoS）
	api.Handle("POST /api/v1/admin/register", rateLimit(loginLimiter)(http.HandlerFunc(adminCtl.Register)))
	api.Handle("POST /api/v1/admin/login", rateLimit(loginLimiter)(http.HandlerFunc(adminCtl.Login)))
	// 两步验证完成登录（预授权凭证 + 验证码），同样严格限流
	api.Handle("POST /api/v1/admin/login/2fa", rateLimit(loginLimiter)(http.HandlerFunc(adminCtl.Login2FA)))
	// 以下均需 JWT 鉴权
	api.Handle("POST /api/v1/admin/logout", auth.Middleware(http.HandlerFunc(adminCtl.Logout)))
	api.Handle("DELETE /api/v1/admin/comment/{id}", auth.Middleware(http.HandlerFunc(adminCtl.Delete)))
	api.Handle("PUT /api/v1/admin/comment/{id}/audit", auth.Middleware(http.HandlerFunc(adminCtl.Audit)))
	api.Handle("POST /api/v1/admin/comment/{id}/reply", auth.Middleware(http.HandlerFunc(adminCtl.Reply)))
	api.Handle("PUT /api/v1/admin/comments/batch-audit", auth.Middleware(http.HandlerFunc(adminCtl.BatchAudit)))
	api.Handle("POST /api/v1/admin/comments/batch-delete", auth.Middleware(http.HandlerFunc(adminCtl.BatchDelete)))
	api.Handle("GET /api/v1/admin/comments/pending", auth.Middleware(http.HandlerFunc(adminCtl.Pending)))
	api.Handle("GET /api/v1/admin/sites", auth.Middleware(http.HandlerFunc(adminCtl.Sites)))

	// ============================================================
	// 并行任务路由锚点区（已预埋，任务开发者在对应区块取消注释并填入自己的 controller）
	// 约定：只在自己的区块内改动，不得改动其他区块
	// ============================================================

	// ----- T1 settings 配置接口 -----
	settingsCtl := controller.NewSettings(svc)
	api.Handle("GET /api/v1/admin/settings", auth.Middleware(http.HandlerFunc(settingsCtl.Get)))
	api.Handle("PUT /api/v1/admin/settings", auth.Middleware(http.HandlerFunc(settingsCtl.Update)))

	// ----- T2 置顶/取消置顶 -----
	pinCtl := controller.NewPin(svc)
	api.Handle("PUT /api/v1/admin/comment/{id}/pin", auth.Middleware(http.HandlerFunc(pinCtl.Pin)))
	api.Handle("PUT /api/v1/admin/comment/{id}/unpin", auth.Middleware(http.HandlerFunc(pinCtl.Unpin)))

	// ----- T3 全量评论列表（状态/关键词筛选） -----
	commentAdminCtl := controller.NewCommentAdmin(svc)
	api.Handle("GET /api/v1/admin/comments", auth.Middleware(http.HandlerFunc(commentAdminCtl.List)))
	api.Handle("GET /api/v1/admin/comments/replied", auth.Middleware(http.HandlerFunc(commentAdminCtl.Replied)))

	// ----- T4 邮件通知（无需路由，见 main.go 注入 svc.Notifier） -----

	// ----- T6 数据导入导出 -----
	migrateCtl := controller.NewMigrate(svc)
	api.Handle("GET /api/v1/admin/export", auth.Middleware(http.HandlerFunc(migrateCtl.Export)))
	api.Handle("POST /api/v1/admin/migrate", auth.Middleware(http.HandlerFunc(migrateCtl.Import)))
	// 一键备份（FR-36）：WAL checkpoint + 数据库文件快照下载
	api.Handle("GET /api/v1/admin/backup", auth.Middleware(http.HandlerFunc(migrateCtl.Backup)))

	// ----- T7 QQ 绑定回调（无需 JWT，state 为绑定凭证） -----
	qqCtl := controller.NewQQ(svc)
	api.HandleFunc("GET /api/v1/admin/qq/callback", qqCtl.Callback)

	// ----- T8 修改密码（需 JWT） -----
	passwordCtl := controller.NewPassword(svc)
	api.Handle("PUT /api/v1/admin/password", auth.Middleware(http.HandlerFunc(passwordCtl.Update)))

	// ----- T9 管理员个人中心 + OAuth 绑定 -----
	profileCtl := controller.NewProfile(svc)
	api.Handle("GET /api/v1/admin/profile", auth.Middleware(http.HandlerFunc(profileCtl.Get)))
	api.Handle("POST /api/v1/admin/oauth/{provider}/start", auth.Middleware(http.HandlerFunc(profileCtl.StartOAuthBind)))
	api.Handle("DELETE /api/v1/admin/oauth/{provider}", auth.Middleware(http.HandlerFunc(profileCtl.Unbind)))
	// 两步验证开关（需 JWT）
	api.Handle("POST /api/v1/admin/2fa/enable", auth.Middleware(http.HandlerFunc(profileCtl.Enable2FA)))
	api.Handle("POST /api/v1/admin/2fa/disable", auth.Middleware(http.HandlerFunc(profileCtl.Disable2FA)))

	// ----- 账号管理（站长审批新账号 / 授予删除权限 / 禁用 / 删除） -----
	api.Handle("GET /api/v1/admin/accounts", auth.Middleware(http.HandlerFunc(adminCtl.Accounts)))
	api.Handle("POST /api/v1/admin/accounts/{id}/approve", auth.Middleware(http.HandlerFunc(adminCtl.ApproveAccount)))
	api.Handle("PUT /api/v1/admin/accounts/{id}/delete-permission", auth.Middleware(http.HandlerFunc(adminCtl.SetAccountDelete)))
	api.Handle("PUT /api/v1/admin/accounts/{id}/disabled", auth.Middleware(http.HandlerFunc(adminCtl.SetAccountDisabled)))
	api.Handle("DELETE /api/v1/admin/accounts/{id}", auth.Middleware(http.HandlerFunc(adminCtl.DeleteAccount)))

	// ----- T10 GitHub OAuth 回调（无需 JWT，state 为绑定凭证） -----
	githubCtl := controller.NewGitHub(svc)
	api.HandleFunc("GET /api/v1/admin/github/callback", githubCtl.Callback)

	// ----- 邮箱验证码登录 / 绑定 -----
	// 发码与登录无需 JWT，但加严格限流（防短信/邮件轰炸）
	emailCtl := controller.NewEmail(svc)
	api.Handle("POST /api/v1/admin/email/send-code", rateLimit(loginLimiter)(http.HandlerFunc(emailCtl.SendCode)))
	api.Handle("POST /api/v1/admin/email/login", rateLimit(loginLimiter)(http.HandlerFunc(emailCtl.Login)))
	// 绑定 / 解绑需 JWT（个人中心操作）
	api.Handle("POST /api/v1/admin/email/bind-send-code", auth.Middleware(http.HandlerFunc(emailCtl.BindSendCode)))
	api.Handle("POST /api/v1/admin/email/bind", auth.Middleware(http.HandlerFunc(emailCtl.Bind)))
	api.Handle("DELETE /api/v1/admin/email", auth.Middleware(http.HandlerFunc(emailCtl.Unbind)))

	// ============================================================

	// 中间件链：Recovery → Logger → CORS → 全局限流 → API
	apiHandler := middleware.Recovery(
		middleware.Logger(cfg.Server.Mode == "debug")(
			middleware.CORS(cfg.CORS.AllowedOrigins)(
				rateLimit(globalLimiter)(api),
			),
		),
	)

	root := http.NewServeMux()
	root.Handle("/", apiHandler)
	// 前端静态资源（本地开发/直连部署用；生产由 Nginx 托管并强缓存）
	root.Handle("/comment/", http.StripPrefix("/comment/", http.FileServer(http.Dir("front"))))
	// 管理后台页面（静态单文件，仅本地直连部署用；生产由 Nginx 托管并限制访问）
	root.Handle("/admin/", http.StripPrefix("/admin/", http.FileServer(http.Dir("front"))))
	// 简短入口：访问 /admin、/comment 直接跳转到对应页面，免输入完整文件名
	root.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/admin.html", http.StatusMovedPermanently)
	})
	root.HandleFunc("/comment", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/comment/comment.html", http.StatusMovedPermanently)
	})
	return root
}

// rateLimit 通用限流中间件包装
func rateLimit(l *middleware.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(utils.ClientIP(r)) {
				utils.Fail(w, utils.CodeErrIPRateLimit)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
