package service

import (
	"strings"

	"dwxcmt/model"
)

// ListAllComments 管理端全量评论列表，支持状态 / 关键词 / 站点过滤。
// status 为 nil 时表示全部；keyword 模糊匹配昵称或内容；site 为空或 "all" 表示全部站点。
// 返回 (列表, 总数, error)，列表项含隐私字段（ToDTO(true)）。
func (s *Service) ListAllComments(page, pageSize int, status *int, keyword, site string) ([]model.CommentDTO, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// 动态 WHERE 全部参数化，防 SQL 注入
	var conds []string
	var args []interface{}
	if status != nil {
		conds = append(conds, "is_audited = ?")
		args = append(args, *status)
	}
	if site != "" && site != "all" {
		conds = append(conds, "site = ?")
		args = append(args, NormalizeSite(site))
	}
	if kw := strings.TrimSpace(keyword); kw != "" {
		conds = append(conds, "(content LIKE ? OR nick LIKE ? OR email LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like, like)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM comments `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append(append([]interface{}{}, args...), pageSize, offset)
	rows, err := s.DB.Query(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments `+where+`
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

// ListAdminReplies 管理端筛选「后台回复的评论」（is_admin = 1 的回复）。
// 支持站点 / 关键词过滤，返回含隐私字段的 DTO。
func (s *Service) ListAdminReplies(page, pageSize int, keyword, site string) ([]model.CommentDTO, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var conds []string
	var args []interface{}
	conds = append(conds, "is_admin = 1")
	if site != "" && site != "all" {
		conds = append(conds, "site = ?")
		args = append(args, NormalizeSite(site))
	}
	if kw := strings.TrimSpace(keyword); kw != "" {
		conds = append(conds, "(content LIKE ? OR nick LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like)
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM comments `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append(append([]interface{}{}, args...), pageSize, offset)
	rows, err := s.DB.Query(
		`SELECT id, page_id, site, nick, email, link, content, parent_id, root_id,
		        like_count, is_audited, is_pinned, is_admin, ip, user_agent, create_time, update_time
		 FROM comments `+where+`
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
