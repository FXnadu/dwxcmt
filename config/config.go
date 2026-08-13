package config

import (
	"errors"
	"fmt"
	"os"

	"dwxcmt/pkg/utils"

	"gopkg.in/yaml.v3"
)

// DefaultJWTSecret 编译期内嵌的默认 JWT 密钥。
// 用于检测用户是否在生产环境中忘记替换密钥；生产部署必须修改此值。
// 同步修改 Default() 中的 JWTSecret 字段。
const DefaultJWTSecret = "change-me-to-a-random-32-char-secret"

// ErrInsecureJWTSecret 仍使用默认密钥的哨兵错误
var ErrInsecureJWTSecret = errors.New("仍使用默认 JWT 密钥，请在 config.yaml 中修改 auth.jwt_secret 为随机字符串（建议 openssl rand -hex 16）")

// Config 全量配置，对应 config/config.yaml
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Database     DatabaseConfig     `yaml:"database"`
	Auth         AuthConfig         `yaml:"auth"`
	QQOAuth      QQOAuthConfig      `yaml:"qq_oauth"`
	GitHubOAuth  GitHubOAuthConfig  `yaml:"github_oauth"`
	SMTP         SMTPConfig         `yaml:"smtp"`
	RateLimit    RateLimitConfig    `yaml:"rate_limit"`
	Comment      CommentConfig      `yaml:"comment"`
	CORS         CORSConfig         `yaml:"cors"`
	TrustedProxy TrustedProxyConfig `yaml:"trusted_proxy"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type AuthConfig struct {
	JWTSecret string `yaml:"jwt_secret"`
	JWTTTL    int64  `yaml:"jwt_ttl"`
}

type QQOAuthConfig struct {
	AppID       string `yaml:"app_id"`
	AppKey      string `yaml:"app_key"`
	RedirectURI string `yaml:"redirect_uri"`
}

type GitHubOAuthConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURI  string `yaml:"redirect_uri"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	UseSSL   bool   `yaml:"use_ssl"`
}

type RateLimitConfig struct {
	RequestsPerSecond int `yaml:"requests_per_second"`
	CommentsPerDay    int `yaml:"comments_per_day"`
}

type CommentConfig struct {
	ContentMaxLength int `yaml:"content_max_length"`
	NickMaxLength    int `yaml:"nick_max_length"`
	MaxPinnedPerPage int `yaml:"max_pinned_per_page"`
	MaxReplyDepth    int `yaml:"max_reply_depth"`
	// AvatarCacheTTL 成功头像缓存 TTL（秒），0=默认 7 天；惰性过期，物理文件由定时清理回收
	AvatarCacheTTL int `yaml:"avatar_cache_ttl"`
	// AvatarCleanIntervalHours 头像缓存清理间隔（小时），0=仅启动时清理一次；默认 720（每月）。
	// 只删除读取路径已失效的文件（过期缓存/标记、残留 tmp、非法命名孤儿），不影响线上命中
	AvatarCleanIntervalHours int `yaml:"avatar_clean_interval_hours"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type TrustedProxyConfig struct {
	// Proxies 可信反代 IP/CIDR 列表；仅当 RemoteAddr 命中时才读取 X-Forwarded-For/X-Real-IP。
	// 常见值：
	//   - 本机开发：["127.0.0.1/32", "::1/128"]
	//   - Nginx 同机部署：["127.0.0.1/32"]
	//   - 前置 CDN：["<CDN出口段CIDR>"]
	// 为空时 **完全不信任** 转发头，直连 RemoteAddr（最安全，但会丢失真实 IP）。
	Proxies []string `yaml:"proxies"`
}

// Default 返回默认配置（config.yaml 缺失或字段缺失时兜底）
func Default() *Config {
	return &Config{
		Server:   ServerConfig{Port: 8080, Mode: "release"},
		Database: DatabaseConfig{Path: "./comment.db"},
		Auth:     AuthConfig{JWTSecret: DefaultJWTSecret, JWTTTL: 86400},
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 5,
			CommentsPerDay:    20,
		},
		Comment: CommentConfig{
			ContentMaxLength: 500,
			NickMaxLength:    20,
			MaxPinnedPerPage: 3,
			MaxReplyDepth:    3,
			AvatarCacheTTL:            7 * 24 * 3600, // 成功头像缓存 7 天
			AvatarCleanIntervalHours: 30 * 24,       // 清理间隔 30 天（每月）
		},
		// 默认只信任本机回环，防止直连场景下伪造 X-Forwarded-For 绕过限流。
		// 生产部署若前置 Nginx/CDN，请在 config.yaml 中追加其出口 IP/CIDR。
		TrustedProxy: TrustedProxyConfig{
			Proxies: []string{"127.0.0.1/32", "::1/128"},
		},
	}
}

// Validate 校验配置安全性与合法性。
//   - JWT 密钥仍用默认值：返回 ErrInsecureJWTSecret
//   - trusted_proxy.proxies 包含非法 CIDR：返回具体错误
//
// 其他维度的校验可后续扩展（端口范围、TTL 合理值等）。
func (c *Config) Validate() error {
	if c.Auth.JWTSecret == "" || c.Auth.JWTSecret == DefaultJWTSecret {
		return ErrInsecureJWTSecret
	}
	for _, p := range c.TrustedProxy.Proxies {
		if _, err := utils.ParseTrustedProxy(p); err != nil {
			return fmt.Errorf("trusted_proxy.proxies 条目 %q 非法: %w", p, err)
		}
	}
	return nil
}

// Load 加载配置文件；文件不存在时返回默认配置
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
