package controller

import (
	"encoding/json"
	"net/http"

	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// SettingsController 站点配置管理接口（FR-23）
type SettingsController struct {
	svc *service.Service
}

// NewSettings 构造配置控制器
func NewSettings(svc *service.Service) *SettingsController {
	return &SettingsController{svc: svc}
}

// Get GET /api/v1/admin/settings?site=default 返回全量配置（合并默认值）
func (c *SettingsController) Get(w http.ResponseWriter, r *http.Request) {
	site := utils.QueryStr(r, "site", "default")
	st, err := c.svc.GetSiteSettings(site)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, st)
}

// Update PUT /api/v1/admin/settings 只更新请求体中传入的字段
func (c *SettingsController) Update(w http.ResponseWriter, r *http.Request) {
	site := utils.QueryStr(r, "site", "default")
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	st, err := c.svc.UpdateSettings(site, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, st)
}
