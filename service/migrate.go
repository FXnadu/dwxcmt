package service

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// MaxImportBytes 导入文件大小上限（5MB）
const MaxImportBytes = 5 * 1024 * 1024

// MaxBackupsToKeep 备份目录保留的最大份数（超出按文件名时间戳删除最旧的）
const MaxBackupsToKeep = 7

// BackupDatabase 一键备份（FR-36）：WAL checkpoint 后将数据库文件整体拷贝到 backups/ 目录。
// 返回备份文件绝对路径。备份为数据库文件级快照，包含 comments/admins/settings/likes/rate_limits 等全部表。
func (s *Service) BackupDatabase() (string, error) {
	// 1) WAL checkpoint(TRUNCATE)：将 WAL 中未落盘的页合并回主库文件并截断 WAL，
	//    保证拷贝出的主库文件是完整一致的最新快照
	if _, err := s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return "", fmt.Errorf("WAL checkpoint 失败: %w", err)
	}

	dbPath := s.Cfg.Database.Path
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	dest := filepath.Join(backupDir, "comment_"+time.Now().Format("20060102_150405")+".db")

	in, err := os.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("打开数据库文件失败: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		os.Remove(dest)
		return "", fmt.Errorf("拷贝数据库失败: %w", err)
	}
	if err := out.Sync(); err != nil {
		return "", fmt.Errorf("备份文件落盘失败: %w", err)
	}
	// 保留最近 MaxBackupsToKeep 份，超出删除最旧；清理失败仅记日志，不阻断本次备份
	if err := s.pruneBackups(backupDir); err != nil {
		log.Printf("[backup] 清理旧备份失败: %v", err)
	}
	return dest, nil
}

// pruneBackups 删除备份目录中最旧的超出保留份数的备份文件。
// 文件名格式固定为 comment_YYYYMMDD_HHMMSS.db，字典序即时间序，直接按名排序。
func (s *Service) pruneBackups(backupDir string) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}
	var backups []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "comment_") && strings.HasSuffix(name, ".db") {
			backups = append(backups, filepath.Join(backupDir, name))
		}
	}
	if len(backups) <= MaxBackupsToKeep {
		return nil
	}
	sort.Strings(backups)
	for _, p := range backups[:len(backups)-MaxBackupsToKeep] {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("删除旧备份 %s 失败: %w", p, err)
		}
	}
	return nil
}

// ExportItem 导出文件单条记录（本系统原生格式）。
// 导入时可用 source=dwxcomment，或因含 pageId 字段被自动识别，保证导出文件可回读。
// 注：v3 迁移已删除 image_url 列（图床功能移除），故不再输出该字段。
type ExportItem struct {
	ID         int64  `json:"id"`
	PageID     string `json:"pageId"`
	Site       string `json:"site"`
	Nick       string `json:"nick"`
	Email      string `json:"email"`
	Link       string `json:"link"`
	Content    string `json:"content"`
	ParentID   int64  `json:"parentId"`
	RootID     int64  `json:"rootId"`
	LikeCount  int    `json:"likeCount"`
	IsAudited  int    `json:"isAudited"`
	IsPinned   int    `json:"isPinned"`
	IsAdmin    int    `json:"isAdmin,omitempty"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	CreateTime int64  `json:"createTime"`
	UpdateTime int64  `json:"updateTime"`
}

// ExportComments 按 site / 时间范围导出评论，流式写出 JSON 数组到 w。
// 逐行编码直接写 w，避免大库全量载入内存造成尖峰（1G 环境）；
// 查询阶段错误直接返回（此时尚未写出任何字节）；写出中途错误仅返回错误由调用方记录。
// startDate/endDate 均可选，支持：Unix 秒、Unix 毫秒、"2006-01-02"、"2006-01-02 15:04:05"、RFC3339；
// 其中日期格式 start 取当天 00:00:00、end 取当天 23:59:59（闭区间）。site 为空或 "all" 表示全部站点。
func (s *Service) ExportComments(w io.Writer, site, startDate, endDate string) error {
	var conds []string
	var args []interface{}
	if site != "" && site != "all" {
		conds = append(conds, "site = ?")
		args = append(args, NormalizeSite(site))
	}
	start, err := parseTimeParam(startDate, true)
	if err != nil {
		return err
	}
	if start > 0 {
		conds = append(conds, "create_time >= ?")
		args = append(args, start)
	}
	end, err := parseTimeParam(endDate, false)
	if err != nil {
		return err
	}
	if end > 0 {
		conds = append(conds, "create_time <= ?")
		args = append(args, end)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	rows, err := s.DB.Query(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments `+where+`
		 ORDER BY create_time ASC, id ASC`, args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	enc := json.NewEncoder(w)
	first := true
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return err
	}
	for rows.Next() {
		var c model.Comment
		if err := scanComment(rows, &c); err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		first = false
		if err := enc.Encode(ExportItem{
			ID: c.ID, PageID: c.PageID, Site: c.Site, Nick: c.Nick, Email: c.Email,
			Link: c.Link, Content: c.Content,
			ParentID: c.ParentID, RootID: c.RootID, LikeCount: c.LikeCount,
			IsAudited: c.IsAudited, IsPinned: c.IsPinned, IsAdmin: c.IsAdmin,
			IP: c.IP, UserAgent: c.UserAgent,
			CreateTime: c.CreateTime, UpdateTime: c.UpdateTime,
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n]\n")
	return err
}

// ImportResult 导入结果
type ImportResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// importRecord 解析后的待导入评论。
// 审核状态：源格式带审核状态时（waline status / 本系统 isAudited）原样保留；
// 未解析到状态时默认 is_audited=1（管理员导入视为已信任）。
type importRecord struct {
	sourceID     string // 源记录标识（用于本批次内父评论映射）
	parentKey    string // 源父评论标识
	nativeParent int64  // 原生格式 parent_id，可直连库中已存在评论
	PageID       string
	Site         string
	Nick         string
	Email        string
	Link         string
	Content      string
	IP           string
	UserAgent    string
	IsPinned     int
	IsAdmin      int
	IsAudited    int  // 0 待审核 / 1 已通过 / -1 垃圾
	auditSet     bool // 是否从源数据解析到审核状态
	LikeCount    int
	CreateTime   int64
	UpdateTime   int64
}

// ImportComments 从 JSON 文件导入评论。
// source 支持 dwxcomment（本系统导出格式）/ waline / twikoo / disqus，其余拒绝（1001）。
// 规则：同 page_id + content 已存在则跳过；非法记录（缺昵称/内容/页码或超长）计入 skipped；
// 父评论先查本批次映射，其次原生格式直连库中已有 id；事务批量写入，失败整体回滚。
func (s *Service) ImportComments(source string, data []byte) (ImportResult, error) {
	var res ImportResult
	if len(data) == 0 {
		return res, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "导入文件为空"}
	}
	if len(data) > MaxImportBytes {
		return res, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "导入文件不能超过 5MB"}
	}

	// 解析 JSON：支持数组或 { "data": [...] } 包装（twikoo 导出常见）
	var raw []map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		var wrapped struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil || wrapped.Data == nil {
			return res, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "导入文件格式错误（应为 JSON 数组）"}
		}
		raw = wrapped.Data
	}

	records, err := parseImportRecords(raw, source)
	if err != nil {
		return res, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	idMap := make(map[string]int64, len(records)) // 源评论标识 → 新评论 id
	for _, rec := range records {
		rec.Nick = strings.TrimSpace(rec.Nick)
		rec.Content = strings.TrimSpace(rec.Content)
		rec.PageID = strings.TrimSpace(rec.PageID)
		if rec.Nick == "" || rec.Content == "" || rec.PageID == "" ||
			utf8.RuneCountInString(rec.Nick) > s.Cfg.Comment.NickMaxLength ||
			utf8.RuneCountInString(rec.Content) > s.Cfg.Comment.ContentMaxLength {
			res.Skipped++
			continue
		}

		// 去重：同 page_id + content 已存在则跳过
		var exists int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM comments WHERE page_id = ? AND content = ?`,
			rec.PageID, rec.Content,
		).Scan(&exists); err != nil {
			return res, err
		}
		if exists > 0 {
			res.Skipped++
			continue
		}

		// 父评论与 rootId 推导
		parentID, rootID := int64(0), int64(0)
		if rec.parentKey != "" {
			if nid, ok := idMap[rec.parentKey]; ok {
				parentID = nid
			} else if rec.nativeParent != 0 {
				var pExists int
				if err := tx.QueryRow(
					`SELECT COUNT(*) FROM comments WHERE id = ?`, rec.nativeParent,
				).Scan(&pExists); err != nil {
					return res, err
				}
				if pExists > 0 {
					parentID = rec.nativeParent
				}
			}
		}
		if parentID != 0 {
			var pParent, pRoot int64
			var pPage, pSite string
			if err := tx.QueryRow(
				`SELECT parent_id, root_id, page_id, site FROM comments WHERE id = ?`, parentID,
			).Scan(&pParent, &pRoot, &pPage, &pSite); err != nil {
				return res, err
			}
			// 父评论必须与本次导入同页同站，否则降级为根评论导入
			// （与正常提交路径 P-8 校验一致，防止跨页 root_id 串台产生幽灵数据）
			if pPage != rec.PageID || NormalizeSite(pSite) != NormalizeSite(rec.Site) {
				parentID, rootID = 0, 0
			} else if pParent == 0 {
				rootID = parentID
			} else {
				rootID = pRoot
			}
		}

		ct := rec.CreateTime
		if ct <= 0 {
			ct = now
		}
		ut := rec.UpdateTime
		if ut <= 0 {
			ut = ct
		}

		// 审核状态：源数据带状态则原样保留（waline approved/waiting/spam、本系统 isAudited），
		// 否则默认已通过（1）——管理员导入视为已信任
		audited := 1
		if rec.auditSet {
			audited = rec.IsAudited
		}

		r, err := tx.Exec(
			`INSERT INTO comments (page_id, site, nick, email, link, content,
			        parent_id, root_id, like_count, is_audited, is_pinned, is_admin, ip, user_agent,
			        create_time, update_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.PageID, NormalizeSite(rec.Site), rec.Nick, rec.Email, rec.Link, rec.Content,
			parentID, rootID, rec.LikeCount, audited, rec.IsPinned, rec.IsAdmin, rec.IP, rec.UserAgent,
			ct, ut,
		)
		if err != nil {
			return res, err
		}
		newID, err := r.LastInsertId()
		if err != nil {
			return res, err
		}
		if rec.sourceID != "" {
			idMap[rec.sourceID] = newID
		}
		res.Imported++
	}

	if err := tx.Commit(); err != nil {
		return res, err
	}

	// 清空相关页面缓存，使新导入评论按各自审核状态立即可见（已通过）或进入待审列表（待审核）
	for _, rec := range records {
		if rec.PageID != "" {
			s.InvalidatePage(NormalizeSite(rec.Site), rec.PageID)
		}
	}
	return res, nil
}

// parseImportRecords 按 source 分发解析。原生导出格式（含 pageId 字段）优先自动识别，保证导出文件可回读。
func parseImportRecords(raw []map[string]interface{}, source string) ([]importRecord, error) {
	if len(raw) > 0 {
		if _, ok := raw[0]["pageId"]; ok {
			return parseNative(raw)
		}
	}
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "dwxcomment":
		return parseNative(raw)
	case "waline":
		return parseWaline(raw)
	case "twikoo":
		return parseTwikoo(raw)
	case "disqus":
		return parseDisqus(raw)
	default:
		return nil, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "不支持的 source: " + source}
	}
}

// parseNative 本系统导出格式（含 id/pageId/parentId/createTime 等）
func parseNative(raw []map[string]interface{}) ([]importRecord, error) {
	records := make([]importRecord, 0, len(raw))
	for _, m := range raw {
		rec := importRecord{
			sourceID:   strField(m, "id"),
			PageID:     strField(m, "pageId"),
			Site:       strField(m, "site"),
			Nick:       strField(m, "nick"),
			Email:      strField(m, "email"),
			Link:       strField(m, "link"),
			Content:    strField(m, "content"),
			IP:         strField(m, "ip"),
			UserAgent:  strField(m, "userAgent"),
			IsPinned:   int(intField(m, "isPinned")),
			IsAdmin:    int(intField(m, "isAdmin")),
			LikeCount:  int(intField(m, "likeCount")),
			CreateTime: intField(m, "createTime"),
			UpdateTime: intField(m, "updateTime"),
		}
		rec.parentKey = strField(m, "parentId")
		rec.nativeParent = intField(m, "parentId")
		// 回读本系统导出格式的审核状态（0 待审核 / 1 已通过 / -1 垃圾）。
		// 仅接受合法取值；越界值（如损坏源文件的 isAudited:5）忽略并回落默认已通过（1），
		// 避免写入既不在公开列表也不在待审队列的不可见孤儿评论。
		if _, ok := m["isAudited"]; ok {
			if v := int(intField(m, "isAudited")); v == 0 || v == 1 || v == -1 {
				rec.IsAudited = v
				rec.auditSet = true
			}
		}
		records = append(records, rec)
	}
	return records, nil
}

// parseWaline Waline 导出格式。
// comment 字段可能是 JSON 字符串或对象，其中含 nick/mail/link/content/ua/url；
// 时间取 insertedAt/createdAt（秒或毫秒）；pid 为父评论 ObjectId。
func parseWaline(raw []map[string]interface{}) ([]importRecord, error) {
	records := make([]importRecord, 0, len(raw))
	for _, m := range raw {
		rec := importRecord{
			sourceID: firstStr(m, "id", "objectId"),
			IP:       strField(m, "ip"),
			IsPinned: boolToInt(m, "isSticky"),
		}
		var commentObj map[string]interface{}
		switch v := m["comment"].(type) {
		case string:
			_ = json.Unmarshal([]byte(v), &commentObj)
		case map[string]interface{}:
			commentObj = v
		}
		rec.Nick = firstStrIn([]map[string]interface{}{commentObj, m}, "nick")
		rec.Email = firstStrIn([]map[string]interface{}{commentObj, m}, "mail")
		rec.Link = firstStrIn([]map[string]interface{}{commentObj, m}, "link")
		rec.Content = firstStrIn([]map[string]interface{}{commentObj, m}, "content")
		rec.UserAgent = firstStrIn([]map[string]interface{}{commentObj, m}, "ua")
		rec.PageID = firstStrIn([]map[string]interface{}{commentObj, m}, "url", "pageId")
		rec.parentKey = strField(m, "pid")
		rec.CreateTime = firstTime(m, "insertedAt", "createdAt")
		rec.UpdateTime = rec.CreateTime
		// 审核状态原样保留：approved→已通过(1)、waiting→待审核(0)、spam→垃圾(-1)
		switch strings.ToLower(strings.TrimSpace(strField(m, "status"))) {
		case "approved":
			rec.IsAudited, rec.auditSet = 1, true
		case "waiting", "pending":
			rec.IsAudited, rec.auditSet = 0, true
		case "spam":
			rec.IsAudited, rec.auditSet = -1, true
		}
		records = append(records, rec)
	}
	return records, nil
}

// parseTwikoo Twikoo 导出格式（数组或 { "data": [...] } 已在外层处理）。
// comment 为对象，含 nick/mail/link/content/ua/url；时间取 created/updated。
func parseTwikoo(raw []map[string]interface{}) ([]importRecord, error) {
	records := make([]importRecord, 0, len(raw))
	for _, m := range raw {
		rec := importRecord{
			sourceID: strField(m, "_id"),
			IP:       strField(m, "ip"),
			IsPinned: boolToInt(m, "isSticky"),
		}
		commentObj, _ := m["comment"].(map[string]interface{})
		rec.Nick = firstStrIn([]map[string]interface{}{commentObj, m}, "nick")
		rec.Email = firstStrIn([]map[string]interface{}{commentObj, m}, "mail")
		rec.Link = firstStrIn([]map[string]interface{}{commentObj, m}, "link")
		rec.Content = firstStrIn([]map[string]interface{}{commentObj, m}, "content")
		rec.UserAgent = firstStrIn([]map[string]interface{}{commentObj, m}, "ua")
		rec.PageID = firstStrIn([]map[string]interface{}{commentObj, m}, "url", "pageId")
		rec.parentKey = firstStr(m, "pid", "parent")
		rec.CreateTime = firstTime(m, "created", "createdAt", "insertedAt")
		rec.UpdateTime = firstTime(m, "updated", "updatedAt")
		if rec.UpdateTime == 0 {
			rec.UpdateTime = rec.CreateTime
		}
		records = append(records, rec)
	}
	return records, nil
}

// parseDisqus Disqus 导出的 JSON 化表示（posts 数组）。
// 单条含 message/createdAt/parent/thread/author{name,email}；pageId 优先取 thread 的 identifier/link。
func parseDisqus(raw []map[string]interface{}) ([]importRecord, error) {
	records := make([]importRecord, 0, len(raw))
	for _, m := range raw {
		rec := importRecord{
			sourceID: strField(m, "id"),
			IP:       firstStr(m, "ipAddress", "ip"),
		}
		author, _ := m["author"].(map[string]interface{})
		thread, _ := m["thread"].(map[string]interface{})
		rec.Nick = firstStrIn([]map[string]interface{}{author, m}, "name", "username", "nick")
		rec.Email = firstStrIn([]map[string]interface{}{author, m}, "email", "mail")
		rec.Content = firstStrIn([]map[string]interface{}{m}, "message", "rawMessage", "content")
		rec.Link = firstStrIn([]map[string]interface{}{thread, m}, "link")
		rec.PageID = firstStrIn([]map[string]interface{}{thread, m}, "identifier", "link", "thread", "pageId")
		rec.parentKey = firstStr(m, "parent")
		rec.CreateTime = firstTime(m, "createdAt", "created", "date")
		rec.UpdateTime = rec.CreateTime
		records = append(records, rec)
	}
	return records, nil
}

// parseTimeParam 解析导出时间过滤参数，返回 Unix 秒；空串返回 0（不参与过滤）。
// 支持：Unix 秒/毫秒、"2006-01-02"（start 取 00:00:00 / end 取 23:59:59）、"2006-01-02 15:04:05"、RFC3339。
func parseTimeParam(v string, isStart bool) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return normalizeTimestamp(n, len(v)), nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, v, time.Local); err == nil {
			if l == "2006-01-02" && !isStart {
				return t.Add(24*time.Hour - time.Second).Unix(), nil
			}
			return t.Unix(), nil
		}
	}
	return 0, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "时间格式不正确: " + v}
}

// normalizeTimestamp 13 位毫秒转秒，其余按秒处理
func normalizeTimestamp(n int64, digits int) int64 {
	if digits >= 13 {
		return n / 1000
	}
	return n
}

// firstTime 从多个字段中读取时间：数字（秒/毫秒）或字符串（数字 / RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"）
func firstTime(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		var s string
		switch t := v.(type) {
		case float64:
			if t >= 1e12 { // 毫秒级时间戳
				return int64(t / 1000)
			}
			return int64(t)
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return normalizeTimestamp(n, len(t.String()))
			}
		case string:
			s = strings.TrimSpace(t)
		case map[string]interface{}:
			// MongoDB 扩展 JSON 常见 {"$date": <ms>}
			if n, ok := t["$date"]; ok {
				switch d := n.(type) {
				case float64:
					return int64(d / 1000)
				case string:
					if ts, err := parseTimeStr(d); err == nil {
						return ts
					}
				}
			}
			continue
		}
		if s == "" {
			continue
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return normalizeTimestamp(n, len(s))
		}
		if ts, err := parseTimeStr(s); err == nil {
			return ts
		}
	}
	return 0
}

// parseTimeStr 解析常见时间字符串（RFC3339 / "2006-01-02 15:04:05" / "2006-01-02"）
func parseTimeStr(s string) (int64, error) {
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, &ErrValidation{Code: utils.CodeErrInvalidParam, Msg: "时间格式不正确: " + s}
}

// strField 读取字符串字段（兼容 string / 数字 / JSON 数字）
func strField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return ""
}

// firstStr 依次读取多个字段，返回第一个非空值
func firstStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v := strField(m, k); v != "" {
			return v
		}
	}
	return ""
}

// firstStrIn 依次从多组 map 中读取字段，返回第一个非空值
func firstStrIn(maps []map[string]interface{}, keys ...string) string {
	for _, m := range maps {
		for _, k := range keys {
			if v := strField(m, k); v != "" {
				return v
			}
		}
	}
	return ""
}

// intField 读取整数/布尔字段
func intField(m map[string]interface{}, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n
		}
	case int:
		return int64(t)
	case int64:
		return t
	case bool:
		if t {
			return 1
		}
	}
	return 0
}

// boolToInt 读取布尔/0-1 字段
func boolToInt(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
	case float64:
		if t != 0 {
			return 1
		}
	case string:
		if strings.EqualFold(strings.TrimSpace(t), "true") || strings.TrimSpace(t) == "1" {
			return 1
		}
	}
	return 0
}
