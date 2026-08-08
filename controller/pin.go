package controller

import (
	"net/http"

	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// PinController 置顶/取消置顶接口
type PinController struct {
	svc *service.Service
}

// NewPin 构造置顶控制器
func NewPin(svc *service.Service) *PinController {
	return &PinController{svc: svc}
}

// Pin PUT /api/v1/admin/comment/{id}/pin
func (c *PinController) Pin(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	isPinned, err := c.svc.PinComment(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"id": id, "isPinned": isPinned})
}

// Unpin PUT /api/v1/admin/comment/{id}/unpin
func (c *PinController) Unpin(w http.ResponseWriter, r *http.Request) {
	id, err := service.ParseID(r.PathValue("id"))
	if err != nil {
		utils.Fail(w, utils.CodeErrInvalidParam)
		return
	}
	isPinned, err := c.svc.UnpinComment(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, map[string]interface{}{"id": id, "isPinned": isPinned})
}
