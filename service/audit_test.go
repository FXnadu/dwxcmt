package service

import (
	"dwxcmt/model"
	"testing"
)

// setAudit 直接修改评论审核状态（insertTestComment 固定 is_audited=1）
func setAudit(t *testing.T, svc *Service, status int, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if _, err := svc.DB.Exec(`UPDATE comments SET is_audited = ? WHERE id = ?`, status, id); err != nil {
			t.Fatalf("设置评论 %d 审核状态失败: %v", id, err)
		}
	}
}

// ===== BatchAuditComments =====

func TestBatchAuditComments_DedupSkipMissing(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 0)
	setAudit(t, svc, 0, 1, 2)

	affected, err := svc.BatchAuditComments([]int64{1, 1, 2, 99999}, 1)
	if err != nil {
		t.Fatalf("批量通过失败: %v", err)
	}
	if affected != 2 {
		t.Errorf("去重 + 跳过不存在后应为 2, got %d", affected)
	}
	for _, id := range []int64{1, 2} {
		var st int
		if err := svc.DB.QueryRow(`SELECT is_audited FROM comments WHERE id = ?`, id).Scan(&st); err != nil {
			t.Fatalf("查询评论 %d 失败: %v", id, err)
		}
		if st != 1 {
			t.Errorf("评论 %d 应改为通过(1), got %d", id, st)
		}
	}
}

func TestBatchAuditComments_Spam(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0) // 默认 is_audited=1

	affected, err := svc.BatchAuditComments([]int64{1}, -1)
	if err != nil {
		t.Fatalf("批量标记垃圾失败: %v", err)
	}
	if affected != 1 {
		t.Errorf("应为 1, got %d", affected)
	}
	var st int
	if err := svc.DB.QueryRow(`SELECT is_audited FROM comments WHERE id = 1`).Scan(&st); err != nil {
		t.Fatalf("查询评论失败: %v", err)
	}
	if st != -1 {
		t.Errorf("评论 1 应改为垃圾(-1), got %d", st)
	}
}

func TestBatchAuditComments_InvalidParams(t *testing.T) {
	svc := testService(t)
	if _, err := svc.BatchAuditComments([]int64{1}, 0); err == nil {
		t.Error("status=0 应返回参数错误")
	}
	if _, err := svc.BatchAuditComments(nil, 1); err == nil {
		t.Error("空 ids 应返回参数错误")
	}
}

// TestBatchAuditComments_InvalidatesPageCache 审核状态影响公开列表可见性：
// 通过(0→1)进入列表、改垃圾(1→-1)移出列表，两种情况都必须失效页面缓存。
func TestBatchAuditComments_InvalidatesPageCache(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0) // is_audited=1 可见

	if _, err := svc.ListComments("/test", "default", 1, 10, "asc"); err != nil {
		t.Fatalf("首次加载列表失败: %v", err)
	}
	if _, err := svc.BatchAuditComments([]int64{1}, -1); err != nil {
		t.Fatalf("批量改垃圾失败: %v", err)
	}
	second, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("二次加载列表失败: %v", err)
	}
	if len(second.Roots) != 0 {
		t.Errorf("改垃圾后列表应为空（缓存已失效）, got %+v", second.Roots)
	}
}

// ===== BatchDeleteComments =====

// TestBatchDeleteComments_CountsCommentsNotCascadeRows 删除计数应为评论条数而非级联行数：
// 根 1 有回复 2、3，根 4 有回复 5，共 5 行数据、2 条根评论，删除后应返回 2。
func TestBatchDeleteComments_CountsCommentsNotCascadeRows(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	insertTestComment(t, svc.DB, 3, 1)
	insertTestComment(t, svc.DB, 4, 0)
	insertTestComment(t, svc.DB, 5, 4)

	deleted, err := svc.BatchDeleteComments([]int64{1, 4})
	if err != nil {
		t.Fatalf("批量删除失败: %v", err)
	}
	if deleted != 2 {
		t.Errorf("应返回评论条数 2（而非级联行数 5）, got %d", deleted)
	}
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if _, err := svc.GetComment(id); err == nil {
			t.Errorf("评论 id=%d 应已被级联删除", id)
		}
	}
}

// TestBatchDeleteComments_RootPlusReply 根与回复同时勾选时，回复随根级联删除，两条都应计入，
// 且计数与勾选顺序无关。
func TestBatchDeleteComments_RootPlusReply(t *testing.T) {
	for _, ids := range [][]int64{{1, 2}, {2, 1}} {
		svc := testService(t)
		insertTestComment(t, svc.DB, 1, 0)
		insertTestComment(t, svc.DB, 2, 1)

		deleted, err := svc.BatchDeleteComments(ids)
		if err != nil {
			t.Fatalf("批量删除失败: %v", err)
		}
		if deleted != 2 {
			t.Errorf("ids=%v 应计数 2, got %d", ids, deleted)
		}
		for _, id := range []int64{1, 2} {
			if _, err := svc.GetComment(id); err == nil {
				t.Errorf("评论 id=%d 应已被删除", id)
			}
		}
	}
}

func TestBatchDeleteComments_DedupSkipMissing(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 0)

	deleted, err := svc.BatchDeleteComments([]int64{1, 1, 2, 99999})
	if err != nil {
		t.Fatalf("批量删除失败: %v", err)
	}
	if deleted != 2 {
		t.Errorf("去重 + 跳过不存在后应为 2, got %d", deleted)
	}
}

func TestBatchDeleteComments_AllMissing(t *testing.T) {
	svc := testService(t)
	deleted, err := svc.BatchDeleteComments([]int64{99999, 88888})
	if err != nil {
		t.Fatalf("全部不存在应返回 nil 错误, got %v", err)
	}
	if deleted != 0 {
		t.Errorf("全部不存在应返回 0, got %d", deleted)
	}
}

func TestBatchDeleteComments_EmptyIDs(t *testing.T) {
	svc := testService(t)
	if _, err := svc.BatchDeleteComments(nil); err == nil {
		t.Error("空 ids 应返回参数错误")
	}
}

// TestBatchDeleteComments_CascadeRemovesLikes 根评论级联删除时，根及其回复的点赞记录都应被清理。
func TestBatchDeleteComments_CascadeRemovesLikes(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	if _, err := svc.LikeComment(1, "127.0.0.1"); err != nil {
		t.Fatalf("给根评论点赞失败: %v", err)
	}
	if _, err := svc.LikeComment(2, "127.0.0.2"); err != nil {
		t.Fatalf("给回复点赞失败: %v", err)
	}

	if _, err := svc.BatchDeleteComments([]int64{1}); err != nil {
		t.Fatalf("批量删除失败: %v", err)
	}
	var n int
	if err := svc.DB.QueryRow(`SELECT COUNT(*) FROM likes WHERE comment_id IN (1, 2)`).Scan(&n); err != nil {
		t.Fatalf("查询 likes 失败: %v", err)
	}
	if n != 0 {
		t.Errorf("级联删除后 likes 应清空, got %d", n)
	}
}

// TestBatchDeleteComments_InvalidatesPageCache 删除会影响公开列表，必须失效页面缓存。
func TestBatchDeleteComments_InvalidatesPageCache(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	if _, err := svc.ListComments("/test", "default", 1, 10, "asc"); err != nil {
		t.Fatalf("首次加载列表失败: %v", err)
	}
	if _, err := svc.BatchDeleteComments([]int64{1}); err != nil {
		t.Fatalf("批量删除失败: %v", err)
	}
	second, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("二次加载列表失败: %v", err)
	}
	if len(second.Roots) != 0 {
		t.Errorf("删除后列表应为空（缓存已失效）, got %+v", second.Roots)
	}
}

// ===== ClearCommentLink =====

// TestClearCommentLink_KeepsCommentClearsLink 去除链接后评论本身保留、link 清空，且前台列表缓存即时失效。
func TestClearCommentLink_KeepsCommentClearsLink(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	if _, err := svc.DB.Exec(`UPDATE comments SET link = 'https://spam.example.com' WHERE id = 1`); err != nil {
		t.Fatalf("预置链接失败: %v", err)
	}

	// 先按带链接状态缓存一份列表；若 ClearCommentLink 未失效缓存，
	// 之后 ListComments 仍会命中这里缓存的旧链接
	first, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("首次加载列表失败: %v", err)
	}
	if len(first.Roots) == 0 {
		t.Fatal("列表应有评论")
	}
	if first.Roots[0].Link == "" {
		t.Fatal("预置链接应可见，测试前提不成立")
	}

	if err := svc.ClearCommentLink(1); err != nil {
		t.Fatalf("去除链接失败: %v", err)
	}

	// 评论本身保留，link 已清空
	c, err := svc.GetComment(1)
	if err != nil {
		t.Fatalf("评论应保留: %v", err)
	}
	if c.Link != "" {
		t.Errorf("link 应被清空, got %q", c.Link)
	}

	// 二次读取：缓存已失效并重建，列表中的链接应为空
	second, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("二次加载列表失败: %v", err)
	}
	if len(second.Roots) == 0 {
		t.Fatal("列表应有评论")
	}
	if second.Roots[0].Link != "" {
		t.Errorf("列表中的 link 应为空（缓存已失效）, got %q", second.Roots[0].Link)
	}
}

func TestClearCommentLink_NotFound(t *testing.T) {
	svc := testService(t)
	if err := svc.ClearCommentLink(99999); err != model.ErrNotFound {
		t.Errorf("不存在的评论应返回 ErrNotFound, got %v", err)
	}
}
