package controller

import (
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"dwxcmt/pkg/utils"
	"dwxcmt/service"
)

// MigrateController 数据导入导出接口（FR-33/34）
type MigrateController struct {
	svc *service.Service
}

// NewMigrate 构造迁移控制器
func NewMigrate(svc *service.Service) *MigrateController {
	return &MigrateController{svc: svc}
}

// Export GET /api/v1/admin/export?site=&start_date=&end_date=
// 返回 JSON 文件流（Content-Disposition: attachment）。
// 流式输出避免大库全量载入内存；导出中途失败仅记录日志（响应可能已部分写出）。
func (c *MigrateController) Export(w http.ResponseWriter, r *http.Request) {
	site := utils.QueryStr(r, "site", "")
	start := utils.QueryStr(r, "start_date", "")
	end := utils.QueryStr(r, "end_date", "")
	filename := "dwx-comment-export-" + time.Now().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := c.svc.ExportComments(w, site, start, end); err != nil {
		log.Printf("[export] 导出失败: %v", err)
	}
}

// Import POST /api/v1/admin/migrate（multipart/form-data：file + source）
func (c *MigrateController) Import(w http.ResponseWriter, r *http.Request) {
	// 请求体上限（5MB 文件 + 表单冗余）
	const maxBody = service.MaxImportBytes + 1<<20
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(maxBody); err != nil {
		utils.FailMsg(w, utils.CodeErrInvalidParam, "导入文件不能超过 5MB")
		return
	}
	source := strings.TrimSpace(r.FormValue("source"))
	if source == "" {
		utils.FailMsg(w, utils.CodeErrInvalidParam, "缺少 source 字段")
		return
	}
	fh, _, err := r.FormFile("file")
	if err != nil {
		utils.FailMsg(w, utils.CodeErrInvalidParam, "缺少 file 文件")
		return
	}
	defer fh.Close()

	// 精确限制文件大小（>5MB 拒绝）
	data, err := io.ReadAll(io.LimitReader(fh, service.MaxImportBytes+1))
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(data) > service.MaxImportBytes {
		utils.FailMsg(w, utils.CodeErrInvalidParam, "导入文件不能超过 5MB")
		return
	}
	res, err := c.svc.ImportComments(source, data)
	if err != nil {
		writeErr(w, err)
		return
	}
	utils.OK(w, res)
}

// Backup GET /api/v1/admin/backup
// 一键备份（FR-36）：WAL checkpoint 后生成数据库文件级快照（backups/ 目录），
// 并以下载形式返回给浏览器。
func (c *MigrateController) Backup(w http.ResponseWriter, r *http.Request) {
	path, err := c.svc.BackupDatabase()
	if err != nil {
		writeErr(w, err)
		return
	}
	filename := filepath.Base(path)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, path)
}
