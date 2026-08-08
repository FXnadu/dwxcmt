package controller

import (
	"net/http"
	"strconv"
	"strings"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// CommentAdminController 管理端全量评论列表接口
type CommentAdminController struct {
	svc *service.Service
}

// NewCommentAdmin 构造管理端评论控制器
func NewCommentAdmin(svc *service.Service) *CommentAdminController {
	return &CommentAdminController{svc: svc}
}

// List GET /api/v1/admin/comments
// status：0 待审 / 1 已通过 / -1 垃圾；不传 = 全部
// keyword：昵称或内容模糊匹配；site：不传或 all = 全部站点
func (c *CommentAdminController) List(w http.ResponseWriter, r *http.Request) {
	page := utils.QueryInt(r, "page", 1)
	pageSize := utils.QueryInt(r, "pageSize", 10)
	keyword := utils.QueryStr(r, "keyword", "")
	site := utils.QueryStr(r, "site", "")

	// status 缺省为 nil（全部）；传入时必须是 0/1/-1，否则拒绝
	var status *int
	if raw := utils.QueryStr(r, "status", ""); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || (n != 0 && n != 1 && n != -1) {
			utils.Fail(w, utils.CodeErrInvalidParam)
			return
		}
		status = &n
	}

	comments, total, err := c.svc.ListAllComments(page, pageSize, status, keyword, site)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 非站长管理员查看时对邮箱/IP 脱敏
	if err := maskNonOwner(r, c.svc, comments); err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{
		"list":       comments,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": service.TotalPages(total, pageSize),
	})
}

// Replied GET /api/v1/admin/comments/replied
// 筛选后台回复的评论（is_admin = 1），支持站点 / 关键词过滤
func (c *CommentAdminController) Replied(w http.ResponseWriter, r *http.Request) {
	page := utils.QueryInt(r, "page", 1)
	pageSize := utils.QueryInt(r, "pageSize", 10)
	keyword := utils.QueryStr(r, "keyword", "")
	site := utils.QueryStr(r, "site", "")

	comments, total, err := c.svc.ListAdminReplies(page, pageSize, keyword, site)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 非站长管理员查看时对邮箱/IP 脱敏
	if err := maskNonOwner(r, c.svc, comments); err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{
		"list":       comments,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": service.TotalPages(total, pageSize),
	})
}

// maskNonOwner 非站长管理员查看评论列表时，对 email / ip 做脱敏处理
func maskNonOwner(r *http.Request, svc *service.Service, list []model.CommentDTO) error {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		return errInvalidToken
	}
	isOwner, err := svc.IsOwner(adminID)
	if err != nil {
		return err
	}
	if isOwner {
		return nil
	}
	for i := range list {
		if list[i].Email != "" {
			list[i].Email = service.MaskEmail(list[i].Email)
		}
		if list[i].IP != "" {
			list[i].IP = maskIP(list[i].IP)
		}
	}
	return nil
}

// maskIP IP 脱敏：IPv4 保留前 3 段，末段遮蔽；IPv6 保留前两段
func maskIP(ip string) string {
	if strings.Contains(ip, ":") {
		parts := strings.Split(ip, ":")
		if len(parts) > 2 {
			return strings.Join(parts[:2], ":") + ":****"
		}
		return "****"
	}
	if idx := strings.LastIndex(ip, "."); idx > 0 {
		return ip[:idx+1] + "***"
	}
	return "***"
}
