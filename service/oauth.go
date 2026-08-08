package service

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// oauthBindStateTTL OAuth 绑定 state 有效期（10 分钟）
const oauthBindStateTTL = 10 * time.Minute

// oauthBindState JWT 载荷：个人中心发起的 OAuth 绑定流程
// 同时充当 OAuth state（防 CSRF）和绑定凭证（标识要绑定的管理员）
type oauthBindState struct {
	Kind     string `json:"kind"`     // 固定 "oauth_bind"
	AdminID  int64  `json:"adminId"`  // 要绑定的管理员 ID
	Provider string `json:"provider"` // "qq" | "github"
	jwt.RegisteredClaims
}

// SignOAuthBindState 为已登录管理员签发 OAuth 绑定 state（JWT）
// 此 state 同时作为 OAuth 的 state 参数（防 CSRF）和绑定凭证
func (s *Service) SignOAuthBindState(adminID int64, provider string) (string, error) {
	now := time.Now()
	claims := oauthBindState{
		Kind:     "oauth_bind",
		AdminID:  adminID,
		Provider: provider,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(adminID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(oauthBindStateTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.JWT.Secret)
}

// ParseOAuthBindState 校验 OAuth 绑定 state；返回 (adminID, provider, err)
// 非绑定 state（如登录流程的随机 state）会返回 error，调用方据此区分流程
func (s *Service) ParseOAuthBindState(state string) (int64, string, error) {
	if state == "" {
		return 0, "", errors.New("empty state")
	}
	token, err := jwt.ParseWithClaims(state, &oauthBindState{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.JWT.Secret, nil
	})
	if err != nil {
		return 0, "", err
	}
	claims, ok := token.Claims.(*oauthBindState)
	if !ok || !token.Valid || claims.Kind != "oauth_bind" {
		return 0, "", errors.New("invalid oauth bind state")
	}
	return claims.AdminID, claims.Provider, nil
}

// BindQQToAdmin 将 QQ openid + 昵称绑定到指定管理员（JWT 绑定流程，无需密码）
func (s *Service) BindQQToAdmin(adminID int64, openid, nickname string) error {
	// 检查 openid 是否已被其他管理员绑定
	existing, err := s.FindAdminByOpenID(openid)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != adminID {
		return &ErrValidation{Code: utils.CodeErrOAuthAlreadyBound, Msg: "该 QQ 已绑定其他账号"}
	}
	_, err = s.DB.Exec(
		`UPDATE admins SET qq_openid = ?, qq_name = ? WHERE id = ?`,
		openid, nickname, adminID,
	)
	return err
}

// BindGitHubToAdmin 将 GitHub ID + 用户名绑定到指定管理员
func (s *Service) BindGitHubToAdmin(adminID int64, githubID, githubName string) error {
	// 检查 GitHub ID 是否已被其他管理员绑定
	existing, err := s.FindAdminByGitHubID(githubID)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != adminID {
		return &ErrValidation{Code: utils.CodeErrOAuthAlreadyBound, Msg: "该 GitHub 账号已绑定其他管理员"}
	}
	_, err = s.DB.Exec(
		`UPDATE admins SET github_openid = ?, github_name = ? WHERE id = ?`,
		githubID, githubName, adminID,
	)
	return err
}

// UnbindOAuth 解绑管理员的指定 OAuth 提供商
func (s *Service) UnbindOAuth(adminID int64, provider string) error {
	var query string
	switch provider {
	case "qq":
		query = `UPDATE admins SET qq_openid = '', qq_name = '' WHERE id = ?`
	case "github":
		query = `UPDATE admins SET github_openid = '', github_name = '' WHERE id = ?`
	default:
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "不支持的 OAuth 提供商: " + provider}
	}
	_, err := s.DB.Exec(query, adminID)
	return err
}

// FindAdminByGitHubID 按 github_openid 查找管理员；未绑定时返回 (nil, nil)
func (s *Service) FindAdminByGitHubID(githubID string) (*model.Admin, error) {
	var a model.Admin
	err := s.DB.QueryRow(
		`SELECT id, username, password_hash, email, qq_openid, qq_name, github_openid, github_name, create_time FROM admins WHERE github_openid = ?`,
		githubID,
	).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Email, &a.QQOpenID, &a.QQName, &a.GitHubOpenID, &a.GitHubName, &a.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
