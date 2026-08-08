package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"dwxcmt/middleware"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// AdminController 管理接口
type AdminController struct {
	svc  *service.Service
	auth *middleware.Auth
}

// NewAdmin 构造管理控制器
func NewAdmin(svc *service.Service, auth *middleware.Auth) *AdminController {
	return &AdminController{svc: svc, auth: auth}
}

type registerReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Register POST /api/v1/admin/register
// 首个注册账号自动成为站长（可直接登录）；后续注册进入待审批，由站长审批通过后方可登录
func (c *AdminController) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	isOwner, err := c.svc.Register(req.Username, req.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	msg := "注册成功，请等待站长审批后登录"
	if isOwner {
		msg = "注册成功，您已成为站长，可直接登录"
	}
	utils.OKMsg(w, msg, map[string]interface{}{})
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login POST /api/v1/admin/login
// 未开启 2FA：返回 { token, expiresIn }；已开启 2FA：返回 { need2FA:true, preAuthToken, maskedEmail }，
// 验证码已自动发送到绑定邮箱，前端展示脱敏邮箱并引导用户输入验证码
func (c *AdminController) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	res, err := c.svc.Login(req.Username, req.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	if res.Need2FA {
		utils.OK(w, map[string]interface{}{
			"need2FA":      true,
			"preAuthToken": res.PreAuthToken,
			"maskedEmail":  res.MaskedEmail,
		})
		return
	}
	utils.OK(w, map[string]interface{}{"token": res.Token, "expiresIn": res.ExpiresIn})
}

type login2FAReq struct {
	PreAuthToken string `json:"preAuthToken"`
	Code         string `json:"code"`
}

// Login2FA POST /api/v1/admin/login/2fa
// 校验预授权凭证 + 邮箱验证码，通过后签发正式 JWT
func (c *AdminController) Login2FA(w http.ResponseWriter, r *http.Request) {
	var req login2FAReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	res, err := c.svc.Complete2FALogin(req.PreAuthToken, req.Code)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"token": res.Token, "expiresIn": res.ExpiresIn})
}

// Logout POST /api/v1/admin/logout（token 加入内存黑名单）
func (c *AdminController) Logout(w http.ResponseWriter, r *http.Request) {
	token := extractBearer(r)
	if exp, ok := TokenExpiry(r); ok && token != "" {
		c.auth.Blacklist.Add(token, exp)
	}
	utils.OK(w, map[string]interface{}{})
}

// Delete DELETE /api/v1/admin/comment/{id}
// 仅站长或站长授予删除权限的管理员可执行
func (c *AdminController) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	canDelete, err := c.svc.AdminCanDelete(adminID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canDelete {
		utils.Fail(w, utils.CodeErrPermission)
		return
	}
	deleted, err := c.svc.DeleteComment(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"deleted": deleted})
}

// Accounts GET /api/v1/admin/accounts 全部管理员账号（站长审批 / 权限管理，仅站长可访问）
func (c *AdminController) Accounts(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	isOwner, err := c.svc.IsOwner(adminID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !isOwner {
		utils.Fail(w, utils.CodeErrPermission)
		return
	}
	list, err := c.svc.ListAdmins()
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"list": list})
}

// ApproveAccount POST /api/v1/admin/accounts/{id}/approve 站长审批通过新注册账号
func (c *AdminController) ApproveAccount(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	targetID, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.ApproveAdmin(adminID, targetID); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "已通过审批", map[string]interface{}{})
}

type setDeletePermReq struct {
	CanDelete bool `json:"canDelete"`
}

// SetAccountDelete PUT /api/v1/admin/accounts/{id}/delete-permission 站长授予/收回删除权限
func (c *AdminController) SetAccountDelete(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	targetID, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	var req setDeletePermReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.SetAdminDelete(adminID, targetID, req.CanDelete); err != nil {
		writeErr(w, err)
		return
	}
	msg := "已收回删除权限"
	if req.CanDelete {
		msg = "已授予删除权限"
	}
	utils.OKMsg(w, msg, map[string]interface{}{})
}

type setDisabledReq struct {
	Disabled bool `json:"disabled"`
}

// SetAccountDisabled PUT /api/v1/admin/accounts/{id}/disabled 站长禁用/启用账号
func (c *AdminController) SetAccountDisabled(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	targetID, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	var req setDisabledReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.SetAdminDisabled(adminID, targetID, req.Disabled); err != nil {
		writeErr(w, err)
		return
	}
	msg := "账号已启用"
	if req.Disabled {
		msg = "账号已禁用"
	}
	utils.OKMsg(w, msg, map[string]interface{}{})
}

// DeleteAccount DELETE /api/v1/admin/accounts/{id} 站长删除指定管理员账号
func (c *AdminController) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	targetID, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.DeleteAdmin(adminID, targetID); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "账号已删除", map[string]interface{}{})
}

type auditReq struct {
	Status int `json:"status"`
}

// Audit PUT /api/v1/admin/comment/{id}/audit
func (c *AdminController) Audit(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	var req auditReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.AuditComment(id, req.Status); err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"id": id, "status": req.Status})
}

type batchAuditReq struct {
	IDs    []int64 `json:"ids"`
	Status int     `json:"status"`
}

// BatchAudit PUT /api/v1/admin/comments/batch-audit 批量审核：ids + status（1 通过 / -1 垃圾）
func (c *AdminController) BatchAudit(w http.ResponseWriter, r *http.Request) {
	var req batchAuditReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if len(req.IDs) == 0 || (req.Status != 1 && req.Status != -1) {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	affected, err := c.svc.BatchAuditComments(req.IDs, req.Status)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"affected": affected})
}

type batchDeleteReq struct {
	IDs []int64 `json:"ids"`
}

// BatchDelete POST /api/v1/admin/comments/batch-delete 批量删除（仅站长或站长授予删除权限的管理员）
func (c *AdminController) BatchDelete(w http.ResponseWriter, r *http.Request) {
	var req batchDeleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if len(req.IDs) == 0 {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	canDelete, err := c.svc.AdminCanDelete(adminID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canDelete {
		utils.Fail(w, utils.CodeErrPermission)
		return
	}
	deleted, err := c.svc.BatchDeleteComments(req.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"deleted": deleted})
}

type replyReq struct {
	Content string `json:"content"`
}

// Reply POST /api/v1/admin/comment/{id}/reply 站长回复评论
// 以站长身份（昵称 = 站点配置的站长昵称 adminNick，前台显示站长徽章）直接已审核入库
func (c *AdminController) Reply(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	var req replyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	newID, err := c.svc.ReplyComment(id, req.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"id": newID})
}

// Pending GET /api/v1/admin/comments/pending?page=&pageSize=&site=
func (c *AdminController) Pending(w http.ResponseWriter, r *http.Request) {
	page := utils.QueryInt(r, "page", 1)
	pageSize := utils.QueryInt(r, "pageSize", 10)
	site := utils.QueryStr(r, "site", "")
	comments, total, err := c.svc.PendingComments(page, pageSize, site)
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

// Sites GET /api/v1/admin/sites 站点列表（轻量版：comments/settings 去重）
func (c *AdminController) Sites(w http.ResponseWriter, r *http.Request) {
	sites, err := c.svc.ListSites()
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"sites": sites})
}

func extractBearer(r *http.Request) string {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
}

// TokenExpiry 读取 JWT 过期时间（登出黑名单用）
func TokenExpiry(r *http.Request) (time.Time, bool) {
	claims, ok := r.Context().Value(middleware.ClaimsKey).(*utils.Claims)
	if !ok || claims.ExpiresAt == nil {
		return time.Time{}, false
	}
	return claims.ExpiresAt.Time, true
}
