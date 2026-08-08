package service

import (
	"errors"
	"testing"
	"time"

	"dwxcmt/model"
	"dwxcmt/pkg/utils"
)

// ===== ReplyComment 站长回复测试 =====

func TestReplyComment_ReplyToRoot_OK(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	id, err := svc.ReplyComment(1, "感谢支持！")
	if err != nil {
		t.Fatalf("站长回复根评论失败: %v", err)
	}
	if id == 0 {
		t.Fatal("站长回复应返回非零 ID")
	}

	got, err := svc.GetComment(id)
	if err != nil {
		t.Fatalf("读取站长回复失败: %v", err)
	}
	if got.IsAdmin != 1 {
		t.Errorf("站长回复 is_admin 应为 1, got %d", got.IsAdmin)
	}
	if got.IsAudited != 1 {
		t.Errorf("站长回复应直接已审核 is_audited=1, got %d", got.IsAudited)
	}
	if got.ParentID != 1 || got.RootID != 1 {
		t.Errorf("回复根评论 parent_id/root_id 应为 1, got parent=%d root=%d", got.ParentID, got.RootID)
	}
	if got.Nick != "站长" {
		t.Errorf("站长回复昵称默认应为「站长」, got %q", got.Nick)
	}
	if got.PageID != "/test" || got.Site != "default" {
		t.Errorf("站长回复应继承目标评论 pageId/site, got page=%q site=%q", got.PageID, got.Site)
	}
}

func TestReplyComment_CustomNick_FromSettings(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	if err := svc.SetSetting("default", KeyAdminNick, "博主"); err != nil {
		t.Fatalf("设置站长昵称失败: %v", err)
	}
	id, err := svc.ReplyComment(1, "你好")
	if err != nil {
		t.Fatalf("站长回复失败: %v", err)
	}
	got, err := svc.GetComment(id)
	if err != nil {
		t.Fatalf("读取站长回复失败: %v", err)
	}
	if got.Nick != "博主" {
		t.Errorf("站长回复昵称应取配置 adminNick, got %q", got.Nick)
	}
}

func TestReplyComment_ReplyToChild_InheritsRootID(t *testing.T) {
	svc := testService(t)
	// root(1) → child(2)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)

	id, err := svc.ReplyComment(2, "回复子评论")
	if err != nil {
		t.Fatalf("站长回复子评论失败: %v", err)
	}
	got, err := svc.GetComment(id)
	if err != nil {
		t.Fatalf("读取站长回复失败: %v", err)
	}
	if got.RootID != 1 {
		t.Errorf("回复子评论 root_id 应继承为 1, got %d", got.RootID)
	}
	if got.ParentID != 2 {
		t.Errorf("回复子评论 parent_id 应为 2, got %d", got.ParentID)
	}
	if got.IsAdmin != 1 || got.IsAudited != 1 {
		t.Errorf("站长回复应 is_admin=1 且 is_audited=1, got admin=%d audited=%d", got.IsAdmin, got.IsAudited)
	}
}

func TestReplyComment_EmptyContent_Rejected(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)

	_, err := svc.ReplyComment(1, "   ")
	if err == nil {
		t.Error("空内容应被拒绝")
	}
}

func TestReplyComment_NonexistentParent_NotFound(t *testing.T) {
	svc := testService(t)
	_, err := svc.ReplyComment(99999, "hello")
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("不存在的父评论应返回 ErrNotFound, got %v", err)
	}
}

func TestReplyComment_UnauditedParent_Rejected(t *testing.T) {
	svc := testService(t)
	// 手动插入 is_audited=0（待审）的评论
	if _, err := svc.DB.Exec(
		`INSERT INTO comments (id, page_id, site, nick, content, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES (1, '/test', 'default', 'tester', 'pending', 0, 0, 0, ?, ?)`,
		time.Now().Unix(), time.Now().Unix(),
	); err != nil {
		t.Fatalf("插入待审评论失败: %v", err)
	}

	_, err := svc.ReplyComment(1, "回复待审评论")
	if err == nil {
		t.Error("回复未通过审核的评论应被拒绝")
	}
	if err != nil {
		var ev *ErrValidation
		if !errors.As(err, &ev) || ev.Code != utils.CodeErrInvalidParam {
			t.Errorf("应返回 CodeErrInvalidParam 校验错误, got %v", err)
		}
	}
}

func TestReplyComment_DepthLimit_Rejected(t *testing.T) {
	svc := testService(t)
	// 构造 3 层嵌套: root(1) → reply(2) → reply(3) → reply(4, depth=3)
	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 1)
	insertTestComment(t, svc.DB, 3, 2)
	insertTestComment(t, svc.DB, 4, 3)

	// 站长回复 depth=3 的评论 → 新评论 depth=4，超过 MaxReplyDepth=3，应被拒绝
	_, err := svc.ReplyComment(4, "too deep")
	if err == nil {
		t.Error("站长回复超过最大深度应被拒绝")
	}
}

func TestReplyComment_ListShowsIsAdmin(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0)
	if _, err := svc.ReplyComment(1, "站长回复"); err != nil {
		t.Fatalf("站长回复失败: %v", err)
	}

	result, err := svc.ListComments("/test", "default", 1, 10, "asc")
	if err != nil {
		t.Fatalf("获取评论列表失败: %v", err)
	}
	if len(result.Children) != 1 {
		t.Fatalf("根评论下应有 1 条站长回复, got %d", len(result.Children))
	}
	c := result.Children[0]
	if c.IsAdmin != 1 {
		t.Errorf("公开列表应返回 isAdmin=1, got %d", c.IsAdmin)
	}
	if c.Email != "" || c.IP != "" || c.UserAgent != "" {
		t.Errorf("公开列表不应泄露隐私字段, got email=%q ip=%q ua=%q", c.Email, c.IP, c.UserAgent)
	}
}
