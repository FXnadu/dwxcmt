package service

import (
	"database/sql"
	"errors"

	"dwxcmt/model"
)

// AdminProfile 管理员个人中心信息
type AdminProfile struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	CreateTime   int64  `json:"createTime"`
	IsOwner      bool   `json:"isOwner"`   // 站长（首个注册账号）
	CanDelete    bool   `json:"canDelete"` // 是否有删除评论权限
	QQBound      bool   `json:"qqBound"`
	QQName       string `json:"qqName,omitempty"`
	GitHubBound  bool   `json:"githubBound"`
	GitHubName   string `json:"githubName,omitempty"`
	EmailBound   bool   `json:"emailBound"`
	Email        string `json:"email,omitempty"`
	TwoFAEnabled bool   `json:"twofaEnabled"` // 是否开启两步验证
}

// GetAdminProfile 获取管理员个人中心信息
func (s *Service) GetAdminProfile(adminID int64) (*AdminProfile, error) {
	var a model.Admin
	err := s.DB.QueryRow(
		`SELECT id, username, email, twofa_enabled, can_delete, is_owner, qq_openid, qq_name, github_openid, github_name, create_time FROM admins WHERE id = ?`,
		adminID,
	).Scan(&a.ID, &a.Username, &a.Email, &a.TwoFAEnabled, &a.CanDelete, &a.IsOwner, &a.QQOpenID, &a.QQName, &a.GitHubOpenID, &a.GitHubName, &a.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p := &AdminProfile{
		ID:           a.ID,
		Username:     a.Username,
		CreateTime:   a.CreateTime,
		IsOwner:      a.IsOwner != 0,
		CanDelete:    a.CanDelete != 0,
		TwoFAEnabled: a.TwoFAEnabled != 0,
	}
	if a.QQOpenID != "" {
		p.QQBound = true
		p.QQName = a.QQName
	}
	if a.GitHubOpenID != "" {
		p.GitHubBound = true
		p.GitHubName = a.GitHubName
	}
	if a.Email != "" {
		p.EmailBound = true
		p.Email = a.Email
	}
	return p, nil
}
