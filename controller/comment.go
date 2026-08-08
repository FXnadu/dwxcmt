package controller

import (
	"encoding/json"
	"net/http"

	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// CommentController 公开评论接口
type CommentController struct {
	svc *service.Service
}

// NewComment 构造评论控制器
func NewComment(svc *service.Service) *CommentController {
	return &CommentController{svc: svc}
}

// Version 服务版本（健康检查返回）
// 默认 dev；发布时通过 go build -ldflags "-X dwxcmt/controller.Version=vX.Y.Z" 注入
var Version = "dev"

// Health GET /api/v1/health
func (c *CommentController) Health(w http.ResponseWriter, r *http.Request) {
	utils.OK(w, map[string]interface{}{"status": "ok", "version": Version})
}

// List GET /api/v1/comments
func (c *CommentController) List(w http.ResponseWriter, r *http.Request) {
	pageID := utils.QueryStr(r, "pageId", "")
	if pageID == "" {
		utils.Fail(w, utils.CodeErrPageIDRequired)
		return
	}
	site := utils.QueryStr(r, "site", "default")
	page := utils.QueryInt(r, "page", 1)
	pageSize := utils.QueryInt(r, "pageSize", 10)
	sort := utils.QueryStr(r, "sort", "asc")

	result, err := c.svc.ListComments(pageID, site, page, pageSize, sort)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, result)
}

// Count GET /api/v1/comments/count?pageId=xxx
func (c *CommentController) Count(w http.ResponseWriter, r *http.Request) {
	pageID := utils.QueryStr(r, "pageId", "")
	if pageID == "" {
		utils.Fail(w, utils.CodeErrPageIDRequired)
		return
	}
	site := utils.QueryStr(r, "site", "default")
	count, err := c.svc.CountComments(pageID, site)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"count": count})
}

// Avatar GET /api/v1/avatars/{id} — QQ 头像代理接口（公开）
// 评论者的 QQ 号不直接出现在头像 URL 中，由服务端代拉腾讯 qlogo 图片；
// 失败返回 404，前端会回退到下一候选头像（Gravatar/Cravatar/字母头像）。
func (c *CommentController) Avatar(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, contentType, err := c.svc.QQAvatar(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// maxSubmitBytes 提交评论请求体上限（64KB，远大于合法字段总和）。
// 防止慢速大 body 长时间占用连接 / 内存（公开接口，无鉴权）。
const maxSubmitBytes = 64 << 10

// Submit POST /api/v1/comment
func (c *CommentController) Submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
	var req service.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	id, audited, err := c.svc.SubmitComment(&req, utils.ClientIP(r), r.UserAgent())
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "评论已提交，审核通过后显示", map[string]interface{}{
		"id":      id,
		"audited": audited == 1,
	})
}

// Like POST /api/v1/comment/{id}/like
func (c *CommentController) Like(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	likeCount, err := c.svc.LikeComment(id, utils.ClientIP(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"likeCount": likeCount})
}

// SiteConfig GET /api/v1/site-config?site=default 公开站点配置（前台展示用）
// 返回站长徽章文案与站长头像，供前台渲染「站长」身份与头像
func (c *CommentController) SiteConfig(w http.ResponseWriter, r *http.Request) {
	site := utils.QueryStr(r, "site", "default")
	st, err := c.svc.GetSiteSettings(site)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{
		"adminBadge":       st.AdminBadge,
		"adminAvatar":      st.AdminAvatar,
		"adminNick":        st.AdminNick,
		"contentMaxLength": c.svc.Cfg.Comment.ContentMaxLength,
	})
}
