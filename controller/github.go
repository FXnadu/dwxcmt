package controller

import (
	"errors"
	"net/http"
	"strconv"

	"dwxcmt/service"
)

// GitHubController GitHub OAuth 回调处理
type GitHubController struct {
	svc *service.Service
}

// NewGitHub 构造 GitHub 控制器
func NewGitHub(svc *service.Service) *GitHubController {
	return &GitHubController{svc: svc}
}

// Callback GET /api/v1/admin/github/callback?code=xxx&state=xxx
// state 为绑定 JWT（个人中心发起），校验后直接绑定到管理员，返回 HTML 页面
func (c *GitHubController) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeOAuthCallbackHTML(w, "github", false, "GitHub 回调参数无效，请重新发起绑定")
		return
	}

	// 解析绑定 state
	adminID, _, err := c.svc.ParseOAuthBindState(state)
	if err != nil {
		writeOAuthCallbackHTML(w, "github", false, "绑定凭证无效或已过期，请重新发起绑定")
		return
	}

	// 获取 GitHub 用户信息
	user, err := c.svc.GitHubGetUser(code)
	if err != nil {
		if errors.Is(err, service.ErrGitHubNotConfigured) {
			writeOAuthCallbackHTML(w, "github", false, oauthNotConfiguredMsg("GitHub"))
			return
		}
		writeOAuthCallbackHTML(w, "github", false, "获取 GitHub 用户信息失败，请稍后重试")
		return
	}

	// 绑定到管理员
	githubID := strconv.FormatInt(user.ID, 10)
	if err := c.svc.BindGitHubToAdmin(adminID, githubID, user.Name); err != nil {
		writeErr2OAuthHTML(w, "github", err)
		return
	}

	writeOAuthCallbackHTML(w, "github", true, "GitHub 绑定成功："+user.Name)
}
