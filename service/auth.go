package service

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// bcryptCost 密码哈希强度（旧代码注释称 cost=12，但 MinCost+2 实际为 6，
// 强度偏低；统一为显式 12，与注册/改密保持一致）
const bcryptCost = 12

// Register 注册管理员，返回是否为首个注册账号（站长）。
// 首个注册账号自动成为站长（is_owner=1，直接可用）；后续注册进入待审批状态
// （is_approved=0），由站长在后台审批通过后方可登录。
func (s *Service) Register(username, password string) (bool, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return false, err
	}
	if err := ValidatePassword(password); err != nil {
		return false, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost) // cost=12
	if err != nil {
		return false, err
	}
	// 原子注册：INSERT...SELECT 内的 EXISTS 子查询在语句开始执行时求值，
	// 配合 SQLite 单写者串行化，并发注册时只有一个能成为站长（避免旧实现
	// SELECT COUNT 再 INSERT 的竞态）。
	_, err = s.DB.Exec(
		`INSERT INTO admins (username, password_hash, can_delete, is_approved, is_owner, create_time)
		 SELECT ?, ?,
		        CASE WHEN EXISTS(SELECT 1 FROM admins) THEN 0 ELSE 1 END,
		        CASE WHEN EXISTS(SELECT 1 FROM admins) THEN 0 ELSE 1 END,
		        CASE WHEN EXISTS(SELECT 1 FROM admins) THEN 0 ELSE 1 END, ?`,
		username, string(hash), time.Now().Unix(),
	)
	if err != nil {
		// 用户名唯一约束冲突 → 「用户名已存在」
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, newValidationErr(utils.CodeErrUsernameTaken)
		}
		return false, err
	}
	// 回读新账号的站长标记（首个注册为 1，其余为 0）
	var isOwner int
	if err := s.DB.QueryRow(`SELECT is_owner FROM admins WHERE username = ?`, username).Scan(&isOwner); err != nil {
		return false, err
	}
	return isOwner != 0, nil
}

// LoginResult 登录结果：未开启 2FA 时返回 Token/ExpiresIn；
// 开启 2FA 时返回 Need2FA=true + PreAuthToken/MaskedEmail（验证码已自动发送到绑定邮箱）
type LoginResult struct {
	Token        string // 正式 JWT（无需 2FA 时）
	ExpiresIn    int64  // JWT 有效期（秒）
	Need2FA      bool   // 是否需要二次验证
	PreAuthToken string // 2FA 预授权凭证（Need2FA=true 时，5 分钟内有效）
	MaskedEmail  string // 脱敏邮箱，用于前端展示验证码发送去向
}

// Login 管理员登录，成功返回登录结果。
// 已开启 2FA 且绑定邮箱时：签发 2FA 预授权凭证并向绑定邮箱自动发送验证码，返回 Need2FA=true，不返回正式 token。
func (s *Service) Login(username, password string) (*LoginResult, error) {
	var admin model.Admin
	err := s.DB.QueryRow(
		`SELECT id, username, password_hash, email, twofa_enabled, can_delete, is_approved, is_disabled, is_owner, qq_openid, qq_name, github_openid, github_name, create_time FROM admins WHERE username = ?`,
		strings.TrimSpace(username)).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &admin.Email, &admin.TwoFAEnabled, &admin.CanDelete, &admin.IsApproved, &admin.IsDisabled, &admin.IsOwner, &admin.QQOpenID, &admin.QQName, &admin.GitHubOpenID, &admin.GitHubName, &admin.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newValidationErr(utils.CodeErrLoginFailed)
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) != nil {
		return nil, newValidationErr(utils.CodeErrLoginFailed)
	}
	// 审批 / 禁用检查
	if err := s.RequireAdminActive(&admin); err != nil {
		return nil, err
	}
	// 开启 2FA：签发 5 分钟预授权凭证并自动发送验证码到绑定邮箱
	// （两步验证约束密码登录路径；邮箱验证码登录/第三方登录本身即持有验证，不受影响）
	if admin.TwoFAEnabled != 0 && admin.Email != "" {
		preToken, err := s.JWT.SignWithTTL(admin.ID, admin.Username, Purpose2FA, twofaPreAuthTTL)
		if err != nil {
			return nil, err
		}
		if err := s.SendEmailCode(admin.Email); err != nil {
			return nil, err
		}
		return &LoginResult{
			Need2FA:      true,
			PreAuthToken: preToken,
			MaskedEmail:  MaskEmail(admin.Email),
		}, nil
	}
	token, err := s.JWT.Sign(admin.ID, admin.Username)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresIn: s.Cfg.Auth.JWTTTL}, nil
}

// ValidateUsername 用户名：1-32 字符
func ValidateUsername(username string) error {
	if username == "" || len([]rune(username)) > 32 {
		return newValidationErr(utils.CodeErrInvalidParam)
	}
	return nil
}

// ValidatePassword 密码：≥8 位，含数字与字母
func ValidatePassword(password string) error {
	if len([]rune(password)) < 8 {
		return newValidationErr(utils.CodeErrInvalidParam)
	}
	hasLetter, hasDigit := false, false
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return newValidationErr(utils.CodeErrInvalidParam)
	}
	return nil
}
