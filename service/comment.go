package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// SubmitRequest 提交评论请求体
type SubmitRequest struct {
	PageID   string `json:"pageId"`
	Site     string `json:"site"`
	Nick     string `json:"nick"`
	Email    string `json:"email"`
	Link     string `json:"link"`
	Content  string `json:"content"`
	ParentID int64  `json:"parentId"`
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// SubmitComment 提交评论（校验 → 反垃圾 → 入库 is_audited=0）
// 返回 (commentID, isAudited, error)
func (s *Service) SubmitComment(req *SubmitRequest, ip, userAgent string) (int64, int, error) {
	req.Nick = strings.TrimSpace(req.Nick)
	req.Content = strings.TrimSpace(req.Content)
	req.Email = strings.TrimSpace(req.Email)
	req.Link = strings.TrimSpace(req.Link)
	req.Site = NormalizeSite(req.Site)

	// 基础校验
	if req.PageID == "" {
		return 0, 0, newValidationErr(utils.CodeErrPageIDRequired)
	}
	if utf8.RuneCountInString(req.PageID) > 255 {
		return 0, 0, newValidationErr(utils.CodeErrInvalidParam)
	}
	if req.Nick == "" || utf8.RuneCountInString(req.Nick) > s.Cfg.Comment.NickMaxLength {
		return 0, 0, newValidationErr(utils.CodeErrNickInvalid)
	}
	if req.Content == "" || utf8.RuneCountInString(req.Content) > s.Cfg.Comment.ContentMaxLength {
		return 0, 0, &ErrValidation{
			Code: utils.CodeErrContentInvalid,
			Msg:  fmt.Sprintf("评论内容长度不符合要求（1~%d 字符）", s.Cfg.Comment.ContentMaxLength),
		}
	}
	if req.Email != "" && !emailRe.MatchString(req.Email) {
		return 0, 0, newValidationErr(utils.CodeErrEmailInvalid)
	}
	if req.Link != "" {
		u, err := url.Parse(req.Link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || utf8.RuneCountInString(req.Link) > 200 {
			return 0, 0, newValidationErr(utils.CodeErrURLInvalid)
		}
	}

	// 父评论校验、rootId 推导与最大回复深度限制
	parentID, rootID := req.ParentID, int64(0)
	if parentID != 0 {
		var pParent, pRoot int64
		var pPage, pSite string
		var pAudited int
		err := s.DB.QueryRow(`SELECT parent_id, root_id, page_id, site, is_audited FROM comments WHERE id = ?`, parentID).
			Scan(&pParent, &pRoot, &pPage, &pSite, &pAudited)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, newValidationErr(utils.CodeErrNotFound)
		}
		if err != nil {
			return 0, 0, err
		}
		// 仅已通过审核的评论可被回复（与站长回复 ReplyComment 语义一致）；
		// 否则回复会挂在未展示的父评论下，成为计入 count 却永不展示的幽灵数据。
		if pAudited != 1 {
			return 0, 0, newValidationErr(utils.CodeErrInvalidParam)
		}
		// P-8 修复：父评论必须与本次回复属于同一页面与站点。
		// 否则回复会挂到异页根评论下：不出现于任何列表（列表按本页 root_id 匹配），
		// 却计入本页 count，产生「幽灵数据」。
		if pPage != req.PageID || NormalizeSite(pSite) != req.Site {
			return 0, 0, newValidationErr(utils.CodeErrInvalidParam)
		}
		if pParent == 0 {
			rootID = parentID
		} else {
			rootID = pRoot
		}
		// P0-3 修复：校验最大回复深度，避免无限嵌套。
		// 深度定义：根评论 = 0，一级回复 = 1，MaxReplyDepth=3 时最深允许 depth=3 的评论提交。
		depth, err := s.commentDepth(parentID)
		if err != nil {
			return 0, 0, err
		}
		if s.Cfg.Comment.MaxReplyDepth > 0 && depth >= s.Cfg.Comment.MaxReplyDepth {
			return 0, 0, &ErrValidation{
				Code: utils.CodeErrInvalidParam,
				Msg:  fmt.Sprintf("回复层级超过最大限制（最大 %d 层）", s.Cfg.Comment.MaxReplyDepth),
			}
		}
	}

	// 反垃圾：重复内容 → 敏感词 → 每日上限（原子检查+递增）。
	// 顺序注意：重复拦截先于配额扣减，避免被拦截的请求白白消耗该 IP 当日配额；
	// 敏感词命中仍入库（is_audited=-1），消耗配额合理。
	dup, err := s.CheckDuplicate(ip, req.PageID, req.Content)
	if err != nil {
		return 0, 0, err
	}
	if dup {
		return 0, 0, newValidationErr(utils.CodeErrDuplicate)
	}
	audited := 0
	if word := s.CheckSensitive(req.Content); word != "" {
		audited = -1 // 命中敏感词 → 标记垃圾，仍进待审列表
	}
	ok, err := s.CheckAndBumpDailyCount(ip)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, newValidationErr(utils.CodeErrDailyLimit)
	}

	now := time.Now().Unix()
	res, err := s.DB.Exec(`
		INSERT INTO comments (page_id, site, nick, email, link, content,
			parent_id, root_id, like_count, is_audited, is_pinned, ip, user_agent,
			create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?, ?)`,
		req.PageID, req.Site, req.Nick, req.Email, req.Link, req.Content,
		parentID, rootID, audited, ip, userAgent, now, now,
	)
	if err != nil {
		return 0, 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	// 异步触发通知（T4 邮件模块；未注入时静默）
	if s.Notifier != nil {
		nc := &model.Comment{
			ID: id, PageID: req.PageID, Site: req.Site, Nick: req.Nick, Email: req.Email,
			Link: req.Link, Content: req.Content,
			ParentID: parentID, RootID: rootID, IsAudited: audited, IP: ip,
			UserAgent: userAgent, CreateTime: now, UpdateTime: now,
		}
		go func() {
			if parentID != 0 {
				if parent, err := s.GetComment(parentID); err == nil {
					s.Notifier.NotifyReply(nc, parent)
				}
			}
			s.Notifier.NotifyNewComment(nc)
		}()
	}
	return id, audited, nil
}

// ReplyComment 站长回复评论（管理后台）。
// 以站长身份直接已审核（is_audited=1, is_admin=1）入库，挂载到目标评论下；
// 昵称取站点配置的站长昵称（adminNick），默认「站长」；回复成功后通知被回复者（若配置邮件）。
func (s *Service) ReplyComment(parentID int64, content string) (int64, error) {
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > s.Cfg.Comment.ContentMaxLength {
		return 0, &ErrValidation{
			Code: utils.CodeErrContentInvalid,
			Msg:  fmt.Sprintf("评论内容长度不符合要求（1~%d 字符）", s.Cfg.Comment.ContentMaxLength),
		}
	}
	parent, err := s.GetComment(parentID)
	if errors.Is(err, model.ErrNotFound) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	// 仅已通过审核的评论可被站长回复（待审/垃圾评论必须先通过）
	if parent.IsAudited != 1 {
		return 0, &ErrValidation{
			Code: utils.CodeErrInvalidParam,
			Msg:  "只有已通过的评论才能回复",
		}
	}

	// rootId 推导：目标为根评论 → rootId=自身；否则沿用父评论的 rootId
	rootID := int64(0)
	if parent.ParentID == 0 {
		rootID = parent.ID
	} else {
		rootID = parent.RootID
		if rootID == 0 {
			// 防御历史/导入数据 root_id 缺失：退化为以父评论自身为根
			rootID = parent.ID
		}
	}
	// 与用户回复一致的最大回复深度限制
	if s.Cfg.Comment.MaxReplyDepth > 0 {
		depth, err := s.commentDepth(parentID)
		if err != nil {
			return 0, err
		}
		if depth >= s.Cfg.Comment.MaxReplyDepth {
			return 0, &ErrValidation{
				Code: utils.CodeErrInvalidParam,
				Msg:  fmt.Sprintf("回复层级超过最大限制（最大 %d 层）", s.Cfg.Comment.MaxReplyDepth),
			}
		}
	}

	// 昵称：优先站点配置的站长昵称（adminNick），未配置时回退「站长」
	nick := "站长"
	st, err := s.GetSiteSettings(parent.Site)
	if err != nil {
		return 0, err
	}
	if st.AdminNick != "" {
		nick = st.AdminNick
	}
	now := time.Now().Unix()
	res, err := s.DB.Exec(`
		INSERT INTO comments (page_id, site, nick, email, link, content,
			parent_id, root_id, like_count, is_audited, is_pinned, is_admin, ip, user_agent,
			create_time, update_time)
		VALUES (?, ?, ?, '', '', ?, ?, ?, 0, 1, 0, 1, '', '', ?, ?)`,
		parent.PageID, parent.Site, nick, content, parentID, rootID, now, now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	// 站长回复即时可见，清除对应文章缓存分页
	s.InvalidatePage(parent.Site, parent.PageID)

	// 异步通知被回复者（T4 邮件模块；未注入或未配置时静默）
	if s.Notifier != nil {
		nc := &model.Comment{
			ID: id, PageID: parent.PageID, Site: parent.Site, Nick: nick,
			Content: content, ParentID: parentID, RootID: rootID,
			IsAudited: 1, IsAdmin: 1, CreateTime: now, UpdateTime: now,
		}
		go s.Notifier.NotifyReply(nc, parent)
	}
	return id, nil
}

// ListResult 评论列表响应（roots + children）
type ListResult struct {
	Roots      []model.CommentDTO `json:"roots"`
	Children   []model.CommentDTO `json:"children"`
	Total      int                `json:"total"`
	Page       int                `json:"page"`
	PageSize   int                `json:"pageSize"`
	TotalPages int                `json:"totalPages"`
}

// ListComments 获取评论列表（仅已审核，置顶优先），带缓存
func (s *Service) ListComments(pageID, site string, page, pageSize int, sort string) (*ListResult, error) {
	site = NormalizeSite(site)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 50 {
		pageSize = 50
	}
	// 排序语义：asc（前端“最新”，默认）→ 最新在前；desc（“倒序”）→ 最旧在前；hot → 按热度。
	// 置顶区固定按 pin_time DESC（后置顶的排最上），与外部排序模式无关。
	// 末尾追加 id 兜底：时间完全并列时顺序依然确定（同秒置顶/同秒发布不会出现随机顺序）。
	orderSQL := "is_pinned DESC, pin_time DESC, create_time DESC, id DESC"
	switch sort {
	case "desc":
		orderSQL = "is_pinned DESC, pin_time DESC, create_time ASC, id ASC"
	case "hot":
		orderSQL = "is_pinned DESC, pin_time DESC, like_count DESC, create_time DESC, id DESC"
	default:
		sort = "asc"
	}

	// 排序语义 v3：置顶区改为按 pin_time 倒序（v2 按 create_time，相同秒数时顺序不稳定），key 加版本避免误命中
	cacheKey := fmt.Sprintf("sortv3:%s:%s:%d:%d:%s", site, pageID, page, pageSize, sort)
	if v, ok := s.Cache.Get(cacheKey); ok {
		if data, ok := v.([]byte); ok {
			var result ListResult
			if err := json.Unmarshal(data, &result); err == nil {
				return &result, nil
			}
		}
	}

	result := &ListResult{Page: page, PageSize: pageSize}

	// 根评论分页（置顶优先 → 时间正序）
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM comments WHERE page_id = ? AND site = ? AND is_audited = 1 AND parent_id = 0`,
		pageID, site,
	).Scan(&result.Total)
	if err != nil {
		return nil, err
	}
	result.TotalPages = int(math.Ceil(float64(result.Total) / float64(pageSize)))
	if result.TotalPages < 1 {
		result.TotalPages = 1
	}

	offset := (page - 1) * pageSize
	rows, err := s.DB.Query(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments
		 WHERE page_id = ? AND site = ? AND is_audited = 1 AND parent_id = 0
		 ORDER BY `+orderSQL+`
		 LIMIT ? OFFSET ?`,
		pageID, site, pageSize, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rootIDs := make([]int64, 0, pageSize)
	for rows.Next() {
		var c model.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		result.Roots = append(result.Roots, c.ToPublic())
		rootIDs = append(rootIDs, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 子评论全量（该页根评论下的所有回复，按时间正序）
	if len(rootIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(rootIDs)), ",")
		args := make([]interface{}, 0, len(rootIDs))
		for _, id := range rootIDs {
			args = append(args, id)
		}
		childRows, err := s.DB.Query(
			`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
			        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
			 FROM comments
			 WHERE root_id IN (`+placeholders+`) AND is_audited = 1
			 ORDER BY create_time ASC`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		defer childRows.Close()
		for childRows.Next() {
			var c model.Comment
			if err := scanComment(childRows, &c); err != nil {
				return nil, err
			}
			result.Children = append(result.Children, c.ToPublic())
		}
		if err := childRows.Err(); err != nil {
			return nil, err
		}
	}

	if data, err := json.Marshal(result); err == nil {
		s.Cache.Set(cacheKey, data)
	}
	return result, nil
}

// CountComments 某页面评论总数（仅已审核）
func (s *Service) CountComments(pageID, site string) (int, error) {
	site = NormalizeSite(site)
	var count int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM comments WHERE page_id = ? AND site = ? AND is_audited = 1`,
		pageID, site,
	).Scan(&count)
	return count, err
}

// LikeWindow 点赞去重时间窗口（1 分钟内同一 IP 对同一评论只能点赞一次）。
// like_count 计数永久累加、绝不重置；这里仅控制去重窗口与过期记录清理。
const LikeWindow = time.Minute

// LikeComment 点赞（IP + likes 表 1 分钟窗口去重，原子递增，幂等）。
// 语义：
//   - 首次点赞：插入去重记录并 +1
//   - 窗口内重复点赞：幂等，返回当前计数，不 +1
//   - 超过窗口再次点赞：刷新去重记录时间并 +1（计数继续累加，永不清零）
//
// 点赞成功会清空该文章列表缓存：like_count 与 hot 排序都依赖列表缓存，
// 若不失效，前台点赞数与热门排序最长滞后一个缓存周期（60s）。
func (s *Service) LikeComment(commentID int64, ip string) (int, error) {
	var site, pageID string
	err := s.DB.QueryRow(`SELECT site, page_id FROM comments WHERE id = ?`, commentID).Scan(&site, &pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, err
	}

	now := time.Now().Unix()
	cutoff := now - int64(LikeWindow/time.Second)
	liked := false

	// 1) 去重记录已存在且超过窗口：刷新时间，视为一次新的有效点赞
	res, err := s.DB.Exec(
		`UPDATE likes SET create_time = ? WHERE comment_id = ? AND ip = ? AND create_time <= ?`,
		now, commentID, ip, cutoff,
	)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n > 0 {
		liked = true
	}

	// 2) 无去重记录：插入新记录（UNIQUE(comment_id, ip) 兜底），首次点赞
	if !liked {
		res, err := s.DB.Exec(`INSERT OR IGNORE INTO likes (comment_id, ip, create_time) VALUES (?, ?, ?)`, commentID, ip, now)
		if err != nil {
			return 0, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return 0, err
		} else if n > 0 {
			liked = true
		}
	}

	// 有效点赞才递增计数；窗口内重复点赞幂等返回当前数
	if liked {
		if _, err := s.DB.Exec(`UPDATE comments SET like_count = like_count + 1, update_time = ? WHERE id = ?`, now, commentID); err != nil {
			return 0, err
		}
		// 点赞数变化影响列表 like_count 与 hot 排序，清缓存即时刷新
		s.InvalidatePage(site, pageID)
	}
	var likeCount int
	if err := s.DB.QueryRow(`SELECT like_count FROM comments WHERE id = ?`, commentID).Scan(&likeCount); err != nil {
		return 0, err
	}
	return likeCount, nil
}

// CacheKeyPrefix 生成某文章缓存前缀（用于审核/删除时清缓存），须与 ListComments 的 cacheKey 前缀一致
func CacheKeyPrefix(site, pageID string) string {
	return "sortv3:" + NormalizeSite(site) + ":" + pageID + ":"
}

// InvalidatePage 清空某文章的所有分页缓存
func (s *Service) InvalidatePage(site, pageID string) {
	s.Cache.DeletePrefix(CacheKeyPrefix(site, pageID))
}

// GetComment 读取单条评论（管理/内部用）
func (s *Service) GetComment(id int64) (*model.Comment, error) {
	var c model.Comment
	err := s.DB.QueryRow(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments WHERE id = ?`, id,
	).Scan(&c.ID, &c.PageID, &c.Site, &c.Nick, &c.Email, &c.Link, &c.Content,
		&c.ParentID, &c.RootID, &c.LikeCount, &c.IsAudited, &c.IsPinned, &c.IsAdmin,
		&c.IP, &c.UserAgent, &c.CreateTime, &c.UpdateTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func scanComment(row interface{ Scan(...interface{}) error }, c *model.Comment) error {
	return row.Scan(&c.ID, &c.PageID, &c.Site, &c.Nick, &c.Email, &c.Link, &c.Content,
		&c.ParentID, &c.RootID, &c.LikeCount, &c.IsAudited, &c.IsPinned, &c.IsAdmin,
		&c.IP, &c.UserAgent, &c.CreateTime, &c.UpdateTime)
}

// ParseID 解析路径中的 int64 ID
func ParseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// commentDepth 计算给定评论的嵌套深度（相对于根评论）。
// 根评论（parent_id=0）深度 = 0；其直接回复深度 = 1，依此类推。
// 新评论回复到 commentID 时，其深度应 = commentDepth(commentID) + 1。
// 因此在 MaxReplyDepth=3 时，当 commentDepth(parent) >= 3 就拒绝，保证新评论深度 ≤ 3。
//
// 为防止环形 parent_id 导致无限循环，设置迭代上限 = MaxReplyDepth + 1
// （正常树形结构深度不会超过 MaxReplyDepth，+1 余量即可，避免极端时几十次串行查询拖慢单核写路径）
func (s *Service) commentDepth(commentID int64) (int, error) {
	maxIter := s.Cfg.Comment.MaxReplyDepth + 1
	if maxIter < 2 {
		maxIter = 2
	}
	depth := 0
	cur := commentID
	for i := 0; i < maxIter; i++ {
		var parentID int64
		err := s.DB.QueryRow(`SELECT parent_id FROM comments WHERE id = ?`, cur).Scan(&parentID)
		if errors.Is(err, sql.ErrNoRows) {
			// 中间某节点被删除（罕见），退化到当前深度，不报错
			return depth, nil
		}
		if err != nil {
			return 0, err
		}
		if parentID == 0 {
			return depth, nil
		}
		depth++
		cur = parentID
	}
	// 超过 maxIter，疑似环形或超深层级，拒绝
	return 0, &ErrValidation{
		Code: utils.CodeErrInvalidParam,
		Msg:  "回复层级过深或存在环形引用",
	}
}
