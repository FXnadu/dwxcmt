package service

import (
	"database/sql"
	"errors"
	"time"

	"dwxcmt/pkg/sensitive"
	"dwxcmt/pkg/utils"
)

// CheckSensitive 敏感词检测，命中返回命中的词
func (s *Service) CheckSensitive(content string) string {
	return sensitive.Detect(content)
}

// CheckDuplicate 重复内容拦截：5 分钟内、同 pageId、同 IP、相同内容
func (s *Service) CheckDuplicate(ip, pageID, content string) (bool, error) {
	var id int64
	cutoff := time.Now().Add(-5 * time.Minute).Unix()
	err := s.DB.QueryRow(
		`SELECT id FROM comments WHERE ip = ? AND page_id = ? AND content = ? AND create_time > ? LIMIT 1`,
		ip, pageID, content, cutoff,
	).Scan(&id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// CheckAndBumpDailyCount 原子执行「检查当日限额并递增」，返回是否允许本次提交。
//
// P1-7 修复：旧实现为 CheckDailyLimit（读）与 BumpDailyCount（写）分离，
// 高并发下两个 goroutine 可同时读到相同 count 并各自 +1，导致实际突破上限。
// 现在将「INSERT OR IGNORE + 条件 UPDATE」放入单个写事务，
// 借助 SQLite 单写者串行化保证并发安全：
//
//  1. INSERT OR IGNORE 确保当日记录存在（count=0 起）
//  2. UPDATE ... WHERE count < 上限 → RowsAffected=1 表示扣减成功
//
// 若递增成功但后续业务失败导致评论未入库，该 IP 的当日计数会多 1（可接受，
// 攻击者无法借此获益，只是少量消耗自身配额）。
func (s *Service) CheckAndBumpDailyCount(ip string) (bool, error) {
	if s.Cfg.RateLimit.CommentsPerDay <= 0 {
		return true, nil
	}
	today := time.Now().Format("2006-01-02")
	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO rate_limits (identifier, date, count) VALUES (?, ?, 0)`,
		ip, today,
	); err != nil {
		return false, err
	}
	res, err := tx.Exec(
		`UPDATE rate_limits SET count = count + 1 WHERE identifier = ? AND date = ? AND count < ?`,
		ip, today, s.Cfg.RateLimit.CommentsPerDay,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// NormalizeSite 空 site 归一化为 default
func NormalizeSite(site string) string {
	if site == "" {
		return "default"
	}
	return site
}

// CleanupStaleData 定时清理过期数据，防止表无限增长（决策 #14「定时清理」落地）：
//   - likes 表：删除超过 LikeWindow（1 分钟）的去重记录。like_count 计数永久保留，不受影响；
//     清理后同一 IP 在窗口外可再次点赞。
//   - rate_limits 表：仅保留最近 30 天的当日限额记录（删除历史计数行）。
func (s *Service) CleanupStaleData(now time.Time) error {
	likeCutoff := now.Add(-LikeWindow).Unix()
	if _, err := s.DB.Exec(`DELETE FROM likes WHERE create_time < ?`, likeCutoff); err != nil {
		return err
	}
	keep := now.AddDate(0, 0, -30).Format("2006-01-02")
	if _, err := s.DB.Exec(`DELETE FROM rate_limits WHERE date < ?`, keep); err != nil {
		return err
	}
	return nil
}

// ErrValidation 参数校验失败（带错误码）
type ErrValidation struct {
	Code int
	Msg  string
}

func (e *ErrValidation) Error() string { return e.Msg }

func newValidationErr(code int) *ErrValidation {
	return &ErrValidation{Code: code, Msg: utils.Message(code)}
}
