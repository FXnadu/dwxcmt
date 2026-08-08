package controller

import (
	"errors"
	"net/http"

	"dwxcmt/service"
)

// QQController QQ 绑定
type QQController struct {
	svc *service.Service
}

// NewQQ 构造 QQ 控制器
func NewQQ(svc *service.Service) *QQController {
	return &QQController{svc: svc}
}

// qqNotConfiguredMsg 未配置 QQ 互联的统一提示
const qqNotConfiguredMsg = "QQ 互联未配置，请在 config.yaml 的 qq_oauth 中填写 app_id / app_key / redirect_uri"

// Callback GET /api/v1/admin/qq/callback?code=xxx&state=xxx
// 个人中心发起 QQ 绑定：state 为绑定凭证 JWT，验证后绑定到对应管理员并返回回调 HTML。
func (c *QQController) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeOAuthCallbackHTML(w, "qq", false, "QQ 绑定回调参数无效，请重新发起绑定")
		return
	}
	adminID, _, bindErr := c.svc.ParseOAuthBindState(state)
	if bindErr != nil {
		writeOAuthCallbackHTML(w, "qq", false, "QQ 绑定状态无效或已过期，请重新发起绑定")
		return
	}
	c.handleBindCallback(w, r, code, adminID)
}

// handleBindCallback 处理个人中心发起的 QQ 绑定回调
func (c *QQController) handleBindCallback(w http.ResponseWriter, r *http.Request, code string, adminID int64) {
	openid, nickname, err := c.svc.QQGetOpenIDAndName(code)
	if err != nil {
		if errors.Is(err, service.ErrQQNotConfigured) {
			writeOAuthCallbackHTML(w, "qq", false, qqNotConfiguredMsg)
			return
		}
		writeOAuthCallbackHTML(w, "qq", false, "获取 QQ 用户信息失败，请稍后重试")
		return
	}
	if err := c.svc.BindQQToAdmin(adminID, openid, nickname); err != nil {
		writeErr2OAuthHTML(w, "qq", err)
		return
	}
	displayName := nickname
	if displayName == "" {
		displayName = "QQ 用户"
	}
	writeOAuthCallbackHTML(w, "qq", true, "QQ 绑定成功："+displayName)
}
