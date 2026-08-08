package service

import (
	"errors"
	"fmt"
	"time"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// PinComment 置顶评论，返回新的 is_pinned 值。
// 同一文章（page_id + site）置顶数达到上限时拒绝（1001）。
func (s *Service) PinComment(id int64) (int, error) {
	c, err := s.GetComment(id)
	if errors.Is(err, model.ErrNotFound) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if c.ParentID != 0 {
		return 0, &ErrValidation{
			Code: utils.CodeErrInvalidParam,
			Msg:  "只有根评论才能置顶",
		}
	}
	if c.IsPinned == 1 {
		// 已是置顶状态，幂等返回
		return 1, nil
	}

	// 单条原子条件 UPDATE：借助标量子查询把「计数检查 + 置顶」合并为一条语句。
	// SQLite 单写者串行化 + 条件更新，保证并发置顶请求不会突破 MaxPinnedPerPage 上限。
	// 注意：不能写 SELECT 1 ... HAVING COUNT(*)，无 GROUP BY 时 SQLite 视为非聚合查询直接报错。
	res, err := s.DB.Exec(
		`UPDATE comments SET is_pinned = 1, update_time = ? WHERE id = ? AND (
			SELECT COUNT(*) FROM comments
			WHERE page_id = ? AND site = ? AND is_pinned = 1 AND parent_id = 0) < ?`,
		time.Now().Unix(), id, c.PageID, c.Site, s.Cfg.Comment.MaxPinnedPerPage,
	)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, err
	} else if n == 0 {
		// 置顶数已达上限（或并发下已被置顶），拒绝
		return 0, &ErrValidation{
			Code: utils.CodeErrInvalidParam,
			Msg:  fmt.Sprintf("每页最多置顶 %d 条", s.Cfg.Comment.MaxPinnedPerPage),
		}
	}
	// 置顶改变列表顺序，清空该文章缓存
	s.InvalidatePage(c.Site, c.PageID)
	return 1, nil
}

// UnpinComment 取消置顶，返回新的 is_pinned 值
func (s *Service) UnpinComment(id int64) (int, error) {
	c, err := s.GetComment(id)
	if errors.Is(err, model.ErrNotFound) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if c.IsPinned == 0 {
		// 已非置顶状态，幂等返回
		return 0, nil
	}

	if _, err := s.DB.Exec(
		`UPDATE comments SET is_pinned = 0, update_time = ? WHERE id = ?`,
		time.Now().Unix(), id,
	); err != nil {
		return 0, err
	}
	s.InvalidatePage(c.Site, c.PageID)
	return 0, nil
}
