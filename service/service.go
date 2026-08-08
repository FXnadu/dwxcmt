package service

import (
	"database/sql"

	"dwxcmt/config"
	"dwxcmt/model"
	"dwxcmt/pkg/cache"
	"dwxcmt/pkg/utils"
)

// Notifier 评论通知接口。由 T4（邮件通知模块）实现并注入；
// 未注入时（nil）静默跳过，不影响主流程。
type Notifier interface {
	// NotifyNewComment 新评论提交后通知管理员
	NotifyNewComment(c *model.Comment)
	// NotifyReply 评论被回复时通知被回复者
	NotifyReply(c *model.Comment, parent *model.Comment)
}

// Service 业务层，聚合数据库、配置、缓存与 JWT 工具
type Service struct {
	DB       *sql.DB
	Cfg      *config.Config
	Cache    *cache.LRU
	JWT      *utils.JWT
	Notifier Notifier
	// emailCodes 邮箱验证码内存存储（单实例自托管，无需 Redis）
	emailCodes *emailCodeStore
}

// New 构造业务层
func New(db *sql.DB, cfg *config.Config, cache *cache.LRU) *Service {
	return &Service{
		DB:         db,
		Cfg:        cfg,
		Cache:      cache,
		JWT:        utils.NewJWT(cfg.Auth.JWTSecret, cfg.Auth.JWTTTL),
		emailCodes: newEmailCodeStore(),
	}
}
