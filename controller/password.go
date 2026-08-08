package controller

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dwxcmt/middleware"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// PasswordController 修改密码接口
type PasswordController struct {
	svc *service.Service
}

// NewPassword 构造修改密码控制器
func NewPassword(svc *service.Service) *PasswordController {
	return &PasswordController{svc: svc}
}

type changePasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// Update PUT /api/v1/admin/password（需 JWT）
func (c *PasswordController) Update(w http.ResponseWriter, r *http.Request) {
	claims, ok := r.Context().Value(middleware.ClaimsKey).(*utils.Claims)
	if !ok {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	adminID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		utils.Fail(w, utils.CodeErrTokenInvalid)
		return
	}
	var req changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	if err := c.svc.ChangePassword(adminID, req.OldPassword, req.NewPassword); err != nil {
		writeErr(w, err)
		return
	}
	utils.OKMsg(w, "密码已修改，下次登录使用新密码", map[string]interface{}{})
}
