package service

import (
	"database/sql"
	"errors"
	"math"
	"time"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// AuditComment 审核评论：status=1 通过，-1 标记垃圾。通过时清空该文章缓存
func (s *Service) AuditComment(id int64, status int) error {
	if status != 1 && status != -1 {
		return newValidationErr(utils.CodeErrInvalidParam)
	}
	c, err := s.GetComment(id)
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(
		`UPDATE comments SET is_audited = ?, update_time = ? WHERE id = ?`,
		status, time.Now().Unix(), id,
	); err != nil {
		return err
	}
	// 审核状态变更（通过/改垃圾）都可能影响公开列表可见性：
	// 通过（0→1）→ 评论进入列表；改垃圾（1→-1）→ 评论应移出列表。
	// 两种情况都需清缓存，否则改垃圾后前台仍能缓存命中看到「幽灵评论」。
	s.InvalidatePage(c.Site, c.PageID)
	return nil
}

// ClearCommentLink 清除评论的网站链接但保留评论本身，适用于链接不适合展示（如未备案/敏感站点）。
// 链接影响前台昵称渲染为外链，清除后需清空该文章缓存即时生效。
func (s *Service) ClearCommentLink(id int64) error {
	c, err := s.GetComment(id)
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(
		`UPDATE comments SET link = '', update_time = ? WHERE id = ?`,
		time.Now().Unix(), id,
	); err != nil {
		return err
	}
	s.InvalidatePage(c.Site, c.PageID)
	return nil
}

// DeleteComment 物理删除评论（根评论级联删除其全部回复）
func (s *Service) DeleteComment(id int64) (int64, error) {
	c, err := s.GetComment(id)
	if errors.Is(err, model.ErrNotFound) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, err
	}

	// 先收集将被删除的评论 ID（根评论含级联子树），
	// 用于清理头像代理的 avatar:email:{id} 内存映射（见 service/avatar.go）
	var ids []int64
	if c.ParentID == 0 {
		rows, err := s.DB.Query(`SELECT id FROM comments WHERE id = ? OR root_id = ?`, id, id)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var cid int64
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return 0, err
			}
			ids = append(ids, cid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	} else {
		ids = []int64{id}
	}

	var res sql.Result
	if c.ParentID == 0 {
		// 根评论：先清理整棵子树（根+全部回复）的点赞记录，再级联删除，
		// 避免子评论的 likes 成为孤儿记录残留
		if _, err := s.DB.Exec(
			`DELETE FROM likes WHERE comment_id IN (SELECT id FROM comments WHERE id = ? OR root_id = ?)`,
			id, id,
		); err != nil {
			return 0, err
		}
		res, err = s.DB.Exec(`DELETE FROM comments WHERE id = ? OR root_id = ?`, id, id)
	} else {
		if _, err := s.DB.Exec(`DELETE FROM likes WHERE comment_id = ?`, id); err != nil {
			return 0, err
		}
		res, err = s.DB.Exec(`DELETE FROM comments WHERE id = ?`, id)
	}
	if err != nil {
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	s.InvalidatePage(c.Site, c.PageID)
	// 同步失效头像内存映射：删除立即生效，关闭「已删评论头像最多可访问 60s」窗口
	for _, cid := range ids {
		s.Cache.Delete(avatarEmailKey(cid))
	}
	return deleted, nil
}

// BatchAuditComments 批量审核评论：status=1 通过，-1 标记垃圾；自动去重并跳过不存在的 ID
func (s *Service) BatchAuditComments(ids []int64, status int) (int, error) {
	if status != 1 && status != -1 {
		return 0, newValidationErr(utils.CodeErrInvalidParam)
	}
	if len(ids) == 0 {
		return 0, newValidationErr(utils.CodeErrInvalidParam)
	}
	seen := make(map[int64]struct{}, len(ids))
	type pageKey struct{ site, pageID string }
	keys := make(map[pageKey]struct{})
	affected := 0
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		c, err := s.GetComment(id)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			// 出错前先失效已处理页面缓存，避免部分更新留在旧缓存中（前台继续看到旧状态）
			for k := range keys {
				s.InvalidatePage(k.site, k.pageID)
			}
			return affected, err
		}
		res, err := s.DB.Exec(
			`UPDATE comments SET is_audited = ?, update_time = ? WHERE id = ?`,
			status, time.Now().Unix(), id,
		)
		if err != nil {
			for k := range keys {
				s.InvalidatePage(k.site, k.pageID)
			}
			return affected, err
		}
		// 读取后若该评论被并发删除，UPDATE 影响 0 行，不计入 affected
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			continue
		}
		affected++
		keys[pageKey{c.Site, c.PageID}] = struct{}{}
	}
	// 审核状态变更影响公开列表可见性，统一清相关页面缓存
	for k := range keys {
		s.InvalidatePage(k.site, k.pageID)
	}
	return affected, nil
}

// BatchDeleteComments 批量物理删除评论（复用单条删除逻辑：根评论级联删除回复）；自动去重并跳过不存在的 ID。
// 返回值统计成功删除的评论条数（每条评论计数 1，不含级联删除的回复行），与前端勾选数语义一致。
func (s *Service) BatchDeleteComments(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, newValidationErr(utils.CodeErrInvalidParam)
	}
	// 第一阶段：快照存在性（去重）。根评论级联删除会带走其全部回复；若回复与其根同时被勾选，
	// 逐条删除时后处理的回复已不存在，直接计数会被跳过导致结果依赖处理顺序。
	// 先统一快照，保证计数 = 勾选且确实存在的评论条数，与顺序无关。
	seen := make(map[int64]struct{}, len(ids))
	exist := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err := s.GetComment(id); err != nil {
			if errors.Is(err, model.ErrNotFound) {
				continue
			}
			return 0, err
		}
		exist = append(exist, id)
	}
	// 第二阶段：逐条物理删除（根级联删除回复）
	for i, id := range exist {
		if _, err := s.DeleteComment(id); err != nil {
			if errors.Is(err, model.ErrNotFound) {
				continue // 快照后被并发删除，跳过
			}
			return int64(i), err // 前 i 条已删除
		}
	}
	return int64(len(exist)), nil
}

// PendingComments 待审列表：仅 is_audited = 0，按创建时间倒序
func (s *Service) PendingComments(page, pageSize int, site string) ([]model.CommentDTO, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	where := `is_audited = 0`
	args := []interface{}{}
	if site != "" && site != "all" {
		where += ` AND site = ?`
		args = append(args, NormalizeSite(site))
	}

	var total int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE `+where, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append(append([]interface{}{}, args...), pageSize, offset)
	rows, err := s.DB.Query(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments WHERE `+where+`
		 ORDER BY create_time DESC
		 LIMIT ? OFFSET ?`, queryArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	comments := make([]model.CommentDTO, 0, pageSize)
	for rows.Next() {
		var c model.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, 0, err
		}
		comments = append(comments, c.ToDTO(true))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return comments, total, nil
}

// TotalPages 计算总页数（分页响应辅助）
func TotalPages(total, pageSize int) int {
	if total == 0 {
		return 1
	}
	return int(math.Ceil(float64(total) / float64(pageSize)))
}
