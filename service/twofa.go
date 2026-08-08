package service

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// 两步验证（邮箱验证码 2FA）参数
const (
	// Purpose2FA 2FA 预授权凭证的 JWT purpose，仅用于完成二次验证，不可访问受保护接口
	Purpose2FA = "2fa"
	// twofaPreAuthTTL 预授权凭证有效期（秒），5 分钟足够完成输码
	twofaPreAuthTTL = 5 * 60
)

// MaskEmail 邮箱脱敏：保留前 1 位与 @ 后域名，中间用 * 遮蔽。
// 例：admin@qq.com → a***@qq.com；单字符用户名保留原字符。
func MaskEmail(email string) string {
	email = NormalizeEmail(email)
	at := strings.Index(email, "@")
	if at <= 1 {
		return email
	}
	return email[:1] + "***" + email[at:]
}

// Enable2FA 开启两步验证。前置条件：已绑定邮箱且 SMTP 可用（验证码需发送到邮箱）。
func (s *Service) Enable2FA(adminID int64) error {
	var email string
	err := s.DB.QueryRow(`SELECT email FROM admins WHERE id = ?`, adminID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	if err != nil {
		return err
	}
	if NormalizeEmail(email) == "" {
		return &ErrValidation{Code: utils.CodeErrEmailNotBound, Msg: "请先绑定邮箱，再开启两步验证"}
	}
	if !s.HasSMTP() {
		return newValidationErr(utils.CodeErrSMTPNotConfigured)
	}
	_, err = s.DB.Exec(`UPDATE admins SET twofa_enabled = 1 WHERE id = ?`, adminID)
	return err
}

// Disable2FA 关闭两步验证
func (s *Service) Disable2FA(adminID int64) error {
	_, err := s.DB.Exec(`UPDATE admins SET twofa_enabled = 0 WHERE id = ?`, adminID)
	return err
}

// Complete2FALogin 完成二次验证登录：
// 校验 2FA 预授权凭证 → 校验绑定邮箱验证码 → 签发正式 JWT。
// 预授权凭证保证只有密码验证通过者才能换取正式 token；验证码一次性使用防重放。
func (s *Service) Complete2FALogin(preAuthToken, code string) (*LoginResult, error) {
	claims, err := s.JWT.Parse(preAuthToken)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, &ErrValidation{Code: utils.CodeErrTokenExpired, Msg: "验证超时，请重新登录"}
		}
		return nil, &ErrValidation{Code: utils.CodeErrTokenInvalid, Msg: "登录凭证无效，请重新登录"}
	}
	if claims.Purpose != Purpose2FA {
		return nil, &ErrValidation{Code: utils.CodeErrTokenInvalid, Msg: "登录凭证无效，请重新登录"}
	}
	adminID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return nil, &ErrValidation{Code: utils.CodeErrTokenInvalid, Msg: "登录凭证无效，请重新登录"}
	}
	var email string
	var isApproved, isDisabled int
	err = s.DB.QueryRow(`SELECT email, is_approved, is_disabled FROM admins WHERE id = ?`, adminID).Scan(&email, &isApproved, &isDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &ErrValidation{Code: utils.CodeErrTokenInvalid, Msg: "登录凭证无效，请重新登录"}
	}
	if err != nil {
		return nil, err
	}
	if isApproved == 0 {
		return nil, newValidationErr(utils.CodeErrNotApproved)
	}
	if isDisabled != 0 {
		return nil, newValidationErr(utils.CodeErrAccountDisabled)
	}
	email = NormalizeEmail(email)
	if email == "" {
		return nil, &ErrValidation{Code: utils.CodeErrEmailNotBound, Msg: "管理员未绑定邮箱，无法完成两步验证"}
	}
	if err := s.VerifyEmailCode(email, code); err != nil {
		return nil, err
	}
	token, err := s.JWT.Sign(adminID, claims.Username)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresIn: s.Cfg.Auth.JWTTTL}, nil
}
