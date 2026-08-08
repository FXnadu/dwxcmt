package controller

import (
	"errors"
	"net/http"
	"strconv"

	"dwxcmt/middleware"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// ProfileController 管理员个人中心
type ProfileController struct {
	svc *service.Service
}

// NewProfile 构造个人中心控制器
func NewProfile(svc *service.Service) *ProfileController {
	return &ProfileController{svc: svc}
}

// Get GET /api/v1/admin/profile（需 JWT）
func (c *ProfileController) Get(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	profile, err := c.svc.GetAdminProfile(adminID)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, profile)
}

// Unbind DELETE /api/v1/admin/oauth/{provider}（需 JWT）
func (c *ProfileController) Unbind(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	provider := r.PathValue("provider")
	if err := c.svc.UnbindOAuth(adminID, provider); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "已解除"+providerLabel(provider)+"绑定", map[string]interface{}{})
}

// StartOAuthBind POST /api/v1/admin/oauth/{provider}/start（需 JWT）
// 返回 OAuth 授权跳转 URL，前端直接 window.location 跳转
func (c *ProfileController) StartOAuthBind(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	provider := r.PathValue("provider")
	state, err := c.svc.SignOAuthBindState(adminID, provider)
	if err != nil {
		writeErr(w, err)
		return
	}
	var authURL string
	switch provider {
	case "qq":
		authURL, err = c.svc.QQAuthorizeURL(state)
	case "github":
		authURL, err = c.svc.GitHubAuthorizeURL(state)
	default:
		utils.FailMsg(w, utils.CodeErrInvalidParam, "不支持的 OAuth 提供商: "+provider)
		return
	}
	if err != nil {
		if errors.Is(err, service.ErrQQNotConfigured) || errors.Is(err, service.ErrGitHubNotConfigured) {
			utils.FailMsg(w, utils.CodeErrInvalidParam, oauthNotConfiguredMsg(provider))
			return
		}
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"authUrl": authURL})
}

// Enable2FA POST /api/v1/admin/2fa/enable（需 JWT）
// 开启邮箱验证码两步验证（前置：已绑定邮箱且 SMTP 可用）
func (c *ProfileController) Enable2FA(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	if err := c.svc.Enable2FA(adminID); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "两步验证已开启，下次密码登录需额外输入邮箱验证码", map[string]interface{}{})
}

// Disable2FA POST /api/v1/admin/2fa/disable（需 JWT）
func (c *ProfileController) Disable2FA(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	if err := c.svc.Disable2FA(adminID); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "两步验证已关闭", map[string]interface{}{})
}

// adminIDFromContext 从 JWT Claims 上下文中提取管理员 ID
func adminIDFromContext(r *http.Request) (int64, error) {
	claims, ok := r.Context().Value(middleware.ClaimsKey).(*utils.Claims)
	if !ok {
		return 0, errInvalidToken
	}
	return strconv.ParseInt(claims.Subject, 10, 64)
}

// providerDisplayName 返回提供商中文标签
func providerLabel(provider string) string {
	switch provider {
	case "qq":
		return "QQ"
	case "github":
		return "GitHub"
	default:
		return provider
	}
}

var errInvalidToken = &service.ErrValidation{Code: utils.CodeErrTokenInvalid, Msg: "登录凭证无效"}
