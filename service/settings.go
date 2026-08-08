package service

import (
	"database/sql"
	"errors"
	"log"
	"sort"
	"time"

	"dwxcmt/pkg/email"
)

// SettingsKey 配置项 key 常量（对应 settings 表，site+key 唯一）
const (
	KeySiteName    = "siteName"
	KeyNoticeEmail = "noticeEmail"
	KeyNotifyNew   = "notifyNewComment"
	KeyNotifyReply = "notifyReply"
	KeyAuditMode   = "auditMode"   // 固定 "all"，不可修改
	KeyAdminBadge  = "adminBadge"  // 站长徽章文案，默认「站长」
	KeyAdminAvatar = "adminAvatar" // 站长头像 URL，默认空 = 使用字母头像
	KeyAdminNick   = "adminNick"   // 站长昵称（站长回复显示），默认「站长」
)

// Settings 全量站点配置（含默认值），与 API 文档 §3.9 字段一一对应
type Settings struct {
	SiteName    string `json:"siteName"`
	NoticeEmail string `json:"noticeEmail"`
	NotifyNew   bool   `json:"notifyNewComment"`
	NotifyReply bool   `json:"notifyReply"`
	AuditMode   string `json:"auditMode"`
	AdminBadge  string `json:"adminBadge"`
	AdminAvatar string `json:"adminAvatar"`
	AdminNick   string `json:"adminNick"`
}

// DefaultSettings 返回默认站点配置
func DefaultSettings() Settings {
	return Settings{
		SiteName:    "",
		NotifyNew:   true,
		NotifyReply: true,
		AuditMode:   "all",
		AdminBadge:  "站长",
		AdminNick:   "站长",
	}
}

// GetSetting 读取单个配置项（不存在的 key 返回默认值，不报错）
func (s *Service) GetSetting(site, key string) (string, error) {
	site = NormalizeSite(site)
	var value string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE site = ? AND key = ?`, site, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting 写入单个配置项（upsert）
func (s *Service) SetSetting(site, key, value string) error {
	site = NormalizeSite(site)
	_, err := s.DB.Exec(`
		INSERT INTO settings (site, key, value, update_time) VALUES (?, ?, ?, ?)
		ON CONFLICT (site, key) DO UPDATE SET value = excluded.value, update_time = excluded.update_time`,
		site, key, value, time.Now().Unix(),
	)
	return err
}

// GetSiteSettings 读取站点全量配置（合并默认值）
func (s *Service) GetSiteSettings(site string) (Settings, error) {
	def := DefaultSettings()
	rows, err := s.DB.Query(`SELECT key, value FROM settings WHERE site = ?`, NormalizeSite(site))
	if err != nil {
		return def, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return def, err
		}
		switch key {
		case KeySiteName:
			def.SiteName = value
		case KeyNoticeEmail:
			def.NoticeEmail = value
		case KeyNotifyNew:
			def.NotifyNew = value == "true" || value == "1"
		case KeyNotifyReply:
			def.NotifyReply = value == "true" || value == "1"
		case KeyAuditMode:
			def.AuditMode = value
		case KeyAdminBadge:
			def.AdminBadge = value
		case KeyAdminAvatar:
			def.AdminAvatar = value
		case KeyAdminNick:
			def.AdminNick = value
		}
	}
	return def, rows.Err()
}

// HasSMTP 判断是否已配置 SMTP（T4 邮件通知前置条件）
func (s *Service) HasSMTP() bool {
	return s.Cfg.SMTP.Host != "" && s.Cfg.SMTP.Username != ""
}

// EmailSettings 实现 email.SettingsLoader：提供邮件通知所需的最小站点配置。
// SMTP 未配置或站点配置读取失败时返回 ok=false，通知静默跳过。
func (s *Service) EmailSettings(site string) (email.SiteSettings, bool) {
	if !s.HasSMTP() {
		return email.SiteSettings{}, false
	}
	st, err := s.GetSiteSettings(site)
	if err != nil {
		log.Printf("[email] 读取站点配置失败 site=%s: %v", site, err)
		return email.SiteSettings{}, false
	}
	return email.SiteSettings{
		SiteName:    st.SiteName,
		NoticeEmail: st.NoticeEmail,
		NotifyNew:   st.NotifyNew,
		NotifyReply: st.NotifyReply,
	}, true
}

// ListSites 返回出现过评论或配置的全部站点（去重排序，始终包含 default）
func (s *Service) ListSites() ([]string, error) {
	set := map[string]struct{}{"default": {}}

	rows, err := s.DB.Query(`SELECT DISTINCT site FROM comments WHERE site <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			return nil, err
		}
		set[NormalizeSite(site)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	srows, err := s.DB.Query(`SELECT DISTINCT site FROM settings WHERE site <> ''`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var site string
		if err := srows.Scan(&site); err != nil {
			return nil, err
		}
		set[NormalizeSite(site)] = struct{}{}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}

	sites := make([]string, 0, len(set))
	for site := range set {
		sites = append(sites, site)
	}
	sort.Strings(sites)
	return sites, nil
}
