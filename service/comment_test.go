package service

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dwxcmt/config"
	"dwxcmt/model"
	"dwxcmt/pkg/cache"
)

// testService 创建临时 SQLite DB 并返回初始化好的 Service，测试结束后自动清理。
func testService(t *testing.T) *Service {
	t.Helper()
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("lc-test-%d.db", time.Now().UnixNano()))
	db, err := model.Open(tmpFile)
	if err != nil {
		t.Fatalf("打开测试 DB 失败: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpFile)
		os.Remove(tmpFile + "-wal")
		os.Remove(tmpFile + "-shm")
	})
	cfg := config.Default()
	cfg.Auth.JWTSecret = "test-secret-32-chars-minimum!!!"
	c := cache.New(64, time.Minute)
	return New(db, cfg, c)
}

// insertTestComment 直接用 SQL 插入一条已审核评论（绕过 SubmitComment 的校验），用于构造测试数据。
// 自动推导 root_id（与真实提交逻辑一致）：回复根评论 → root_id=根评论 id；回复子评论 → 继承父 root_id。
func insertTestComment(t *testing.T, db *sql.DB, id, parentID int64) {
	t.Helper()
	now := time.Now().Unix()
	var rootID int64
	if parentID != 0 {
		var pParent, pRoot int64
		if err := db.QueryRow(`SELECT parent_id, root_id FROM comments WHERE id = ?`, parentID).Scan(&pParent, &pRoot); err != nil {
			t.Fatalf("查询父评论失败: %v", err)
		}
		if pParent == 0 {
			rootID = parentID
		} else {
			rootID = pRoot
		}
	}
	_, err := db.Exec(
		`INSERT INTO comments (id, page_id, site, nick, content, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		id, "/test", "default", "tester", "test content", parentID, rootID, now, now,
	)
	if err != nil {
		t.Fatalf("插入测试评论 id=%d 失败: %v", id, err)
	}
}

// ===== P0-3: commentDepth 单元测试 =====

func TestCommentDepth_Root(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	depth, err := svc.commentDepth(1)
	if err != nil {
		t.Fatalf("commentDepth(根评论) 失败: %v", err)
	}
	if depth != 0 {
		t.Errorf("根评论深度应为 0, got %d", depth)
	}
}

func TestCommentDepth_Nested(t *testing.T) {
	svc := testService(t)
	// 链路: 1(root) → 2 → 3 → 4
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	insertTestComment(t, svc.DB, 3, 2)
	insertTestComment(t, svc.DB, 4, 3)

	cases := []struct {
		id   int64
		want int
	}{
		{1, 0},
		{2, 1},
		{3, 2},
		{4, 3},
	}
	for _, c := range cases {
		depth, err := svc.commentDepth(c.id)
		if err != nil {
			t.Fatalf("commentDepth(%d) 失败: %v", c.id, err)
		}
		if depth != c.want {
			t.Errorf("commentDepth(%d) = %d, want %d", c.id, depth, c.want)
		}
	}
}

func TestCommentDepth_NonExistent(t *testing.T) {
	svc := testService(t)
	// 不存在的评论 ID → sql.ErrNoRows → 返回 depth=0 不报错
	depth, err := svc.commentDepth(99999)
	if err != nil {
		t.Fatalf("不存在的评论应返回 depth=0 不报错, got err: %v", err)
	}
	if depth != 0 {
		t.Errorf("不存在的评论深度应为 0, got %d", depth)
	}
}

// ===== P0-3: SubmitComment 回复深度限制集成测试 =====

func TestSubmitComment_DepthLimitExceeded(t *testing.T) {
	svc := testService(t)
	// 构造 3 层嵌套: root(1) → reply(2) → reply(3) → reply(4, depth=3)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	insertTestComment(t, svc.DB, 3, 2)
	insertTestComment(t, svc.DB, 4, 3) // depth=3, MaxReplyDepth=3

	// 回复 depth=3 的评论 → 新评论 depth=4，超过 MaxReplyDepth=3，应被拒绝
	req := &SubmitRequest{
		PageID:   "/test",
		Site:     "default",
		Nick:     "tester",
		Content:  "reply to depth 3 should fail",
		ParentID: 4,
	}
	_, _, err := svc.SubmitComment(req, "127.0.0.1", "test-agent")
	if err == nil {
		t.Error("回复 depth=3 的评论应被拒绝（MaxReplyDepth=3），但 SubmitComment 返回 nil")
	}
}

func TestSubmitComment_DepthAtLimit_OK(t *testing.T) {
	svc := testService(t)
	// 构造 2 层嵌套: root(1) → reply(2) → reply(3, depth=2)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	insertTestComment(t, svc.DB, 3, 2) // depth=2

	// 回复 depth=2 的评论 → 新评论 depth=3，等于 MaxReplyDepth=3，应通过
	req := &SubmitRequest{
		PageID:   "/test",
		Site:     "default",
		Nick:     "tester",
		Content:  "reply to depth 2 should pass",
		ParentID: 3,
	}
	id, _, err := svc.SubmitComment(req, "127.0.0.1", "test-agent")
	if err != nil {
		t.Errorf("回复 depth=2 的评论应成功, got err: %v", err)
	}
	if id == 0 {
		t.Error("成功提交应返回非零 ID")
	}
}

func TestSubmitComment_RootComment_OK(t *testing.T) {
	svc := testService(t)
	// 根评论（无父评论）→ depth=0，应通过
	req := &SubmitRequest{
		PageID:  "/test",
		Site:    "default",
		Nick:    "tester",
		Content: "root comment",
	}
	id, _, err := svc.SubmitComment(req, "127.0.0.1", "test-agent")
	if err != nil {
		t.Errorf("根评论应成功, got err: %v", err)
	}
	if id == 0 {
		t.Error("成功提交应返回非零 ID")
	}
}

func TestSubmitComment_ReplyToRoot_OK(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	// 回复根评论 → depth=1，应通过
	req := &SubmitRequest{
		PageID:   "/test",
		Site:     "default",
		Nick:     "tester",
		Content:  "reply to root",
		ParentID: 1,
	}
	id, _, err := svc.SubmitComment(req, "127.0.0.1", "test-agent")
	if err != nil {
		t.Errorf("回复根评论应成功, got err: %v", err)
	}
	if id == 0 {
		t.Error("成功提交应返回非零 ID")
	}
}

// TestLikeComment_InvalidatesPageCache 验证点赞成功后会清空列表缓存：
// 若未失效，第二次 ListComments 会命中缓存返回旧 likeCount=0。
func TestLikeComment_InvalidatesPageCache(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0) // 已审核根评论

	// 首次加载列表，写入缓存（likeCount=0）
	first, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("首次加载列表失败: %v", err)
	}
	if len(first.Roots) != 1 || first.Roots[0].LikeCount != 0 {
		t.Fatalf("初始列表应含 1 条评论且 likeCount=0, got %+v", first.Roots)
	}

	// 点赞后再次加载：若缓存未清，仍返回 likeCount=0
	if _, err := svc.LikeComment(1, "127.0.0.1"); err != nil {
		t.Fatalf("点赞失败: %v", err)
	}
	second, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("二次加载列表失败: %v", err)
	}
	if len(second.Roots) != 1 || second.Roots[0].LikeCount != 1 {
		t.Fatalf("点赞后列表应 likeCount=1（缓存已失效）, got %+v", second.Roots)
	}
}
