package controller

import (
	"errors"
	"log"
	"net/http"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// writeErr 统一错误映射：业务错误 → 对应错误码；未知/数据库错误 → 500
func writeErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var ve *service.ErrValidation
	if errors.As(err, &ve) {
		utils.FailMsg(w, ve.Code, ve.Msg)
		return
	}
	if errors.Is(err, model.ErrNotFound) {
		utils.Fail(w, utils.CodeErrNotFound)
		return
	}
	log.Printf("[error] %v", err)
	utils.Error(w, http.StatusInternalServerError, utils.CodeErrInternal)
}
