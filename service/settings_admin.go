package service

import (
	"strings"
	"unicode/utf8"

	"dwxcmt/pkg/utils"
)

// settingsWhitelist PUT 允许更新的配置 key（auditMode 单独拦截，不可修改）
var settingsWhitelist = map[string]bool{
	KeySiteName:    true,
	KeyNoticeEmail: true,
	KeyNotifyNew:   true,
	KeyNotifyReply: true,
	KeyAdminBadge:  true,
	KeyAdminAvatar: true,
	KeyAdminNick:   true,
}

// UpdateSettings 更新站点配置：只更新请求体中传入的字段，返回更新后的全量配置（合并默认值）。
// - auditMode 固定 "all"，传入即拒绝（1001）
// - 未知 key 拒绝（1001）
// - 布尔存 "true"/"false"
func (s *Service) UpdateSettings(site string, updates map[string]interface{}) (Settings, error) {
	site = NormalizeSite(site)
	for key, val := range updates {
		if key == KeyAuditMode {
			return Settings{}, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "auditMode 固定为 all，不可修改"}
		}
		if !settingsWhitelist[key] {
			return Settings{}, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "未知配置项: " + key}
		}
		strVal, err := settingsValueToString(key, val)
		if err != nil {
			return Settings{}, err
		}
		if err := s.SetSetting(site, key, strVal); err != nil {
			return Settings{}, err
		}
	}
	return s.GetSiteSettings(site)
}

// settingsValueToString 将请求值转换为 settings 表可存的字符串
func settingsValueToString(key string, val interface{}) (string, error) {
	switch key {
	case KeyNotifyNew, KeyNotifyReply:
		switch v := val.(type) {
		case bool:
			if v {
				return "true", nil
			}
			return "false", nil
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1":
				return "true", nil
			case "false", "0":
				return "false", nil
			}
		}
		return "", &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: key + " 必须是布尔值"}
	case KeySiteName, KeyNoticeEmail, KeyAdminBadge, KeyAdminAvatar, KeyAdminNick:
		v, ok := val.(string)
		if !ok {
			return "", &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: key + " 必须是字符串"}
		}
		v = strings.TrimSpace(v)
		// noticeEmail 需为合法邮箱，否则邮件通知将投递失败
		if key == KeyNoticeEmail && v != "" {
			if err := ValidateEmail(v); err != nil {
				return "", err
			}
		}
		// 各字段长度上限，防止非法配置占用 / 破坏邮件与前台展示
		maxLen := 64
		switch key {
		case KeyAdminAvatar:
			maxLen = 500
		case KeyNoticeEmail:
			maxLen = 254
		}
		if utf8.RuneCountInString(v) > maxLen {
			return "", &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: key + " 长度超出限制"}
		}
		return v, nil
	}
	return "", &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "未知配置项: " + key}
}
