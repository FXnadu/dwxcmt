package controller

import (
	"encoding/json"
	"net/http"

	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// EmailController 邮箱验证码登录 / 绑定
type EmailController struct {
	svc *service.Service
}

// NewEmail 构造邮箱登录控制器
func NewEmail(svc *service.Service) *EmailController {
	return &EmailController{svc: svc}
}

type emailCodeReq struct {
	Email string `json:"email"`
}

type emailLoginReq struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// SendCode POST /api/v1/admin/email/send-code
// 登录页/绑定页通用发码接口（限流由路由层控制）；SMTP 未配置时明确提示
func (c *EmailController) SendCode(w http.ResponseWriter, r *http.Request) {
	var req emailCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.sendCode(req.Email); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "验证码已发送到邮箱，10 分钟内有效", map[string]interface{}{})
}

// Login POST /api/v1/admin/email/login
// 请求体 { "email": "xxx", "code": "123456" } → 校验通过且邮箱已绑定则签发 JWT
func (c *EmailController) Login(w http.ResponseWriter, r *http.Request) {
	var req emailLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	token, ttl, err := c.svc.LoginByEmail(req.Email, req.Code)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"token": token, "expiresIn": ttl})
}

// BindSendCode POST /api/v1/admin/email/bind-send-code（需 JWT）
// 个人中心绑定邮箱时发码
func (c *EmailController) BindSendCode(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	var req emailCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	// 绑定前预检邮箱唯一性，避免用户等待邮件后才收到失败
	if err := service.ValidateEmail(req.Email); err != nil {
		writeErr(w, err)
		return
	}
	existing, err := c.svc.FindAdminByEmail(req.Email)
	if err != nil {
		writeErr(w, err)
		return
	}
	if existing != nil && existing.ID != adminID {
		writeErr(w, &service.ErrValidation{Code: utils.CodeErrEmailAlreadyBound, Msg: utils.Message(utils.CodeErrEmailAlreadyBound)})
		return
	}
	if err := c.sendCode(req.Email); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "验证码已发送到邮箱，10 分钟内有效", map[string]interface{}{})
}

// Bind POST /api/v1/admin/email/bind（需 JWT）
// 请求体 { "email": "xxx", "code": "123456" } → 校验验证码后绑定邮箱
func (c *EmailController) Bind(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	var req emailLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.VerifyEmailCode(req.Email, req.Code); err != nil {
		writeErr(w, err)
		return
	}
	if err := c.svc.BindEmailToAdmin(adminID, req.Email); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "邮箱绑定成功，下次可直接使用邮箱验证码登录", map[string]interface{}{})
}

// Unbind DELETE /api/v1/admin/email（需 JWT）
func (c *EmailController) Unbind(w http.ResponseWriter, r *http.Request) {
	adminID, err := adminIDFromContext(r)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	if err := c.svc.UnbindEmail(adminID); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "已解除邮箱绑定", map[string]interface{}{})
}

// sendCode 委托 service 生成并发送验证码（同步发送，失败时作废验证码）
func (c *EmailController) sendCode(toAddr string) error {
	return c.svc.SendEmailCode(toAddr)
}
