package service

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"dwxcmt/model"
	"dwxcmt/pkg/email"
	"dwxcmt/pkg/utils"
)

// 邮箱验证码参数
const (
	emailCodeTTL         = 10 * time.Minute // 验证码有效期
	emailCodeCooldown    = 60 * time.Second // 同邮箱发送冷却
	emailCodeDailyMax    = 10               // 同邮箱每日发送上限
	emailCodeMaxAttempts = 5                // 同一验证码连续校验失败上限，超限作废（防暴力破解）
	emailStoreMaxEntries = 1024             // 内存 map 条目阈值，超过即触发过期清理（防无界增长）
)

// emailCodeEntry 单条验证码记录
type emailCodeEntry struct {
	code      string
	expiresAt time.Time
	failCount int // 连续校验失败次数，达到上限作废验证码
}

// emailCodeStore 内存验证码存储（单实例自托管场景，无需 Redis）
type emailCodeStore struct {
	mu        sync.Mutex
	codes     map[string]emailCodeEntry // key: 小写邮箱
	cooldowns map[string]time.Time      // key: 小写邮箱 -> 上次发送时间
	dailyDate string                    // 今日日期串（"2006-01-02"）
	dailyCnt  map[string]int            // key: 小写邮箱 -> 今日发送次数
}

func newEmailCodeStore() *emailCodeStore {
	return &emailCodeStore{
		codes:     make(map[string]emailCodeEntry),
		cooldowns: make(map[string]time.Time),
		dailyCnt:  make(map[string]int),
		dailyDate: time.Now().Format("2006-01-02"),
	}
}

// pruneLocked 清理过期验证码与已失效冷却记录，防止 map 无界增长。
// 必须在持锁状态下调用：
//   - codes 中超过有效期的条目已不可用（校验时必然失败），可安全删除；
//   - cooldowns 中冷却已结束的条目不再参与任何判断，可安全删除。
func (s *emailCodeStore) pruneLocked(now time.Time) {
	for k, e := range s.codes {
		if now.After(e.expiresAt) {
			delete(s.codes, k)
		}
	}
	for k, last := range s.cooldowns {
		if now.After(last.Add(emailCodeCooldown)) {
			delete(s.cooldowns, k)
		}
	}
}

// NormalizeEmail 规范化邮箱（小写 + 去空白）
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail 校验邮箱格式（简单规则，满足大多数场景）
func ValidateEmail(email string) error {
	email = NormalizeEmail(email)
	if email == "" || len(email) > 254 || !strings.Contains(email, "@") {
		return &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "邮箱格式无效"}
	}
	return nil
}

// GenerateEmailCode 为指定邮箱生成验证码并存储；校验冷却与每日上限。
// 返回验证码，由调用方负责发送邮件。SMTP 未配置时返回错误（调用方提示）。
func (s *Service) GenerateEmailCode(email string) (string, error) {
	if err := ValidateEmail(email); err != nil {
		return "", err
	}
	if !s.HasSMTP() {
		return "", newValidationErr(utils.CodeErrSMTPNotConfigured)
	}
	key := NormalizeEmail(email)
	now := time.Now()
	store := s.emailCodes
	store.mu.Lock()
	defer store.mu.Unlock()

	// 跨天重置每日计数
	if today := now.Format("2006-01-02"); today != store.dailyDate {
		store.dailyDate = today
		store.dailyCnt = make(map[string]int)
	}

	// 防无界增长：条目超阈值时清理过期验证码与失效冷却记录
	if len(store.codes)+len(store.cooldowns) > emailStoreMaxEntries {
		store.pruneLocked(now)
	}

	// 冷却检查
	if last, ok := store.cooldowns[key]; ok && now.Before(last.Add(emailCodeCooldown)) {
		return "", newValidationErr(utils.CodeErrEmailCooldown)
	}
	// 每日上限
	if store.dailyCnt[key] >= emailCodeDailyMax {
		return "", newValidationErr(utils.CodeErrEmailDailyLimit)
	}

	code, err := randomDigits(6)
	if err != nil {
		return "", err
	}
	store.codes[key] = emailCodeEntry{code: code, expiresAt: now.Add(emailCodeTTL)}
	store.cooldowns[key] = now
	store.dailyCnt[key]++
	return code, nil
}

// SendEmailCode 生成验证码并同步发送到邮箱。
// 与 Notifier 通知的异步「尽力而为」策略不同：验证码必须送达，
// 发送失败时作废验证码并返回错误（调用方提示用户稍后重试）。
func (s *Service) SendEmailCode(toAddr string) error {
	code, err := s.GenerateEmailCode(toAddr)
	if err != nil {
		return err
	}
	from := strings.TrimSpace(s.Cfg.SMTP.Username)
	if from == "" {
		from = "no-reply@localhost"
	}
	subject := "【dwxComment】验证码"
	body := "您的验证码为：" + code + "\n\n验证码 10 分钟内有效，请勿将验证码告知他人。若非本人操作，请忽略本邮件。\n"
	if err := email.Send(s.Cfg.SMTP, from, NormalizeEmail(toAddr), subject, body, ""); err != nil {
		s.InvalidateEmailCode(toAddr)
		return &ErrValidation{Code: utils.CodeErrInternal, Msg: "验证码邮件发送失败，请稍后重试"}
	}
	return nil
}

// VerifyEmailCode 校验验证码；成功后立即删除（一次性），防重放
func (s *Service) VerifyEmailCode(email, code string) error {
	key := NormalizeEmail(email)
	store := s.emailCodes
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, ok := store.codes[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return newValidationErr(utils.CodeErrEmailCodeInvalid)
	}
	if entry.code != strings.TrimSpace(code) {
		entry.failCount++
		if entry.failCount >= emailCodeMaxAttempts {
			// 连续失败超限：作废该验证码，防止无限暴力尝试
			delete(store.codes, key)
		} else {
			store.codes[key] = entry
		}
		return newValidationErr(utils.CodeErrEmailCodeInvalid)
	}
	delete(store.codes, key)
	return nil
}

// InvalidateEmailCode 使指定邮箱的验证码立即失效（邮件发送失败时调用，避免无效码残留）
func (s *Service) InvalidateEmailCode(email string) {
	key := NormalizeEmail(email)
	s.emailCodes.mu.Lock()
	defer s.emailCodes.mu.Unlock()
	delete(s.emailCodes.codes, key)
}

// FindAdminByEmail 按邮箱查找管理员；未绑定时返回 (nil, nil)
func (s *Service) FindAdminByEmail(email string) (*model.Admin, error) {
	var a model.Admin
	err := s.DB.QueryRow(
		`SELECT id, username, password_hash, email, twofa_enabled, is_approved, is_disabled, qq_openid, qq_name, github_openid, github_name, create_time FROM admins WHERE email = ?`,
		NormalizeEmail(email),
	).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Email, &a.TwoFAEnabled, &a.IsApproved, &a.IsDisabled, &a.QQOpenID, &a.QQName, &a.GitHubOpenID, &a.GitHubName, &a.CreateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// BindEmailToAdmin 将邮箱绑定到指定管理员（校验邮箱唯一）
func (s *Service) BindEmailToAdmin(adminID int64, email string) error {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}
	existing, err := s.FindAdminByEmail(email)
	if err != nil {
		return err
	}
	if existing != nil && existing.ID != adminID {
		return newValidationErr(utils.CodeErrEmailAlreadyBound)
	}
	_, err = s.DB.Exec(`UPDATE admins SET email = ? WHERE id = ?`, email, adminID)
	return err
}

// UnbindEmail 解绑管理员邮箱（解绑后 2FA 无法收码，一并关闭）
func (s *Service) UnbindEmail(adminID int64) error {
	_, err := s.DB.Exec(`UPDATE admins SET email = '', twofa_enabled = 0 WHERE id = ?`, adminID)
	return err
}

// LoginByEmail 邮箱验证码登录：校验验证码 → 查找绑定管理员 → 签发 JWT。
// 返回 (token, ttl秒)。
func (s *Service) LoginByEmail(email, code string) (string, int64, error) {
	if err := s.VerifyEmailCode(email, code); err != nil {
		return "", 0, err
	}
	admin, err := s.FindAdminByEmail(email)
	if err != nil {
		return "", 0, err
	}
	if admin == nil {
		// 与验证码错误返回相同文案，避免通过错误差异枚举已绑定管理员的邮箱
		return "", 0, newValidationErr(utils.CodeErrEmailCodeInvalid)
	}
	// 审批 / 禁用检查
	if err := s.RequireAdminActive(admin); err != nil {
		return "", 0, err
	}
	token, err := s.JWT.Sign(admin.ID, admin.Username)
	if err != nil {
		return "", 0, err
	}
	return token, s.Cfg.Auth.JWTTTL, nil
}

// randomDigits 生成 n 位数字验证码（crypto/rand，防预测）
func randomDigits(n int) (string, error) {
	const digits = "0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		b[i] = digits[idx.Int64()]
	}
	return string(b), nil
}
