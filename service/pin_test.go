package service

import (
	"testing"
)

// TestPinComment_NewestPinFirst 验证置顶区按「置顶时间」倒序：
// 后置顶的评论必须排在先置顶的前面（即使两条 create_time 相同）。
func TestPinComment_NewestPinFirst(t *testing.T) {
	svc := testService(t)

	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 0)

	if _, err := svc.PinComment(1); err != nil {
		t.Fatalf("置顶 id=1 失败: %v", err)
	}
	if _, err := svc.PinComment(2); err != nil {
		t.Fatalf("置顶 id=2 失败: %v", err)
	}

	res, err := svc.ListComments("/test", "default", 1, 50, "asc")
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if len(res.Roots) != 2 {
		t.Fatalf("期望 2 条根评论，实际 %d", len(res.Roots))
	}
	if res.Roots[0].ID != 2 || res.Roots[1].ID != 1 {
		t.Fatalf("置顶顺序错误：期望 [2, 1]，实际 [%d, %d]", res.Roots[0].ID, res.Roots[1].ID)
	}
}

// TestPinComment_RepinMovesToTop 验证：取消置顶后恢复为普通评论；再次置顶按新的
// 置顶时间顶到第一位。显式设置 create_time，避免同秒并列导致顺序不确定。
func TestPinComment_RepinMovesToTop(t *testing.T) {
	svc := testService(t)

	insertTestComment(t, svc.DB, 2, 0)
	insertTestComment(t, svc.DB, 1, 0)
	// 1 比 2 晚创建：即使 pin_time 同秒并列，create_time 倒序也保证 1 在前
	for _, q := range []string{
		`UPDATE comments SET create_time = 1000 WHERE id = 2`,
		`UPDATE comments SET create_time = 2000 WHERE id = 1`,
	} {
		if _, err := svc.DB.Exec(q); err != nil {
			t.Fatalf("设置 create_time 失败: %v", err)
		}
	}

	if _, err := svc.PinComment(2); err != nil {
		t.Fatalf("置顶 id=2 失败: %v", err)
	}
	if _, err := svc.PinComment(1); err != nil {
		t.Fatalf("置顶 id=1 失败: %v", err)
	}

	// 1 后置顶 → 排第一
	assertRootOrder(t, svc, []int64{1, 2}, "置顶后")

	// 取消 1 的置顶 → 2 置顶排前，1 落回普通区
	if _, err := svc.UnpinComment(1); err != nil {
		t.Fatalf("取消置顶 id=1 失败: %v", err)
	}
	assertRootOrder(t, svc, []int64{2, 1}, "取消置顶后")

	// 1 重新置顶 → pin_time 最新，顶到第一位
	if _, err := svc.PinComment(1); err != nil {
		t.Fatalf("再次置顶 id=1 失败: %v", err)
	}
	assertRootOrder(t, svc, []int64{1, 2}, "重新置顶后")
}

func assertRootOrder(t *testing.T, svc *Service, want []int64, stage string) {
	t.Helper()
	res, err := svc.ListComments("/test", "default", 1, 50, "asc")
	if err != nil {
		t.Fatalf("%s：查询列表失败: %v", stage, err)
	}
	if len(res.Roots) != len(want) {
		t.Fatalf("%s：期望 %d 条根评论，实际 %d", stage, len(want), len(res.Roots))
	}
	for i, id := range want {
		if res.Roots[i].ID != id {
			t.Fatalf("%s：顺序错误：期望 %v，实际 [%d, %d]", stage, want, res.Roots[0].ID, res.Roots[1].ID)
		}
	}
}

// TestPinComment_SameSecondPin 模拟两条评论「同一秒发布 + 同一秒置顶」的极端场景
// （复现用户报告的问题）：pin_time 与 create_time 全部并列时，靠末尾 id 兜底得到
// 确定顺序；任一评论晚一秒置顶，则严格按「后置顶的排最上」。
func TestPinComment_SameSecondPin(t *testing.T) {
	svc := testService(t)

	insertTestComment(t, svc.DB, 1, 0)
	insertTestComment(t, svc.DB, 2, 0)
	// 同秒发布 + 同秒置顶：create_time、pin_time 完全相等
	for _, q := range []string{
		`UPDATE comments SET create_time = 1000 WHERE id IN (1, 2)`,
		`UPDATE comments SET is_pinned = 1, pin_time = 5000 WHERE id IN (1, 2)`,
	} {
		if _, err := svc.DB.Exec(q); err != nil {
			t.Fatalf("模拟同秒发布/置顶失败: %v", err)
		}
	}

	// 时间全部并列时，id 兜底：后插入的 id=2 排第一
	first := rootOrder(t, svc)
	t.Logf("同秒置顶顺序（pin_time/create_time 并列 → id 兜底）: %v", first)
	if len(first) != 2 || first[0] != 2 || first[1] != 1 {
		t.Fatalf("同秒并列时应按 id 兜底 [2, 1]，实际 %v", first)
	}
	// 绕过缓存再查一次，确认真实查询结果稳定可复现（不再随机）
	svc.InvalidatePage("default", "/test")
	again := rootOrder(t, svc)
	if len(again) != 2 || again[0] != 2 || again[1] != 1 {
		t.Fatalf("同秒并列结果应稳定可复现，两次查询不一致：%v / %v", first, again)
	}

	// id=1 比 id=2 晚一秒置顶（5001 > 5000）→ 后置顶的 id=1 必须顶到最上
	if _, err := svc.DB.Exec(`UPDATE comments SET pin_time = 5001 WHERE id = 1`); err != nil {
		t.Fatalf("模拟晚一秒置顶失败: %v", err)
	}
	svc.InvalidatePage("default", "/test")
	got := rootOrder(t, svc)
	t.Logf("延迟一秒置顶后顺序（按 pin_time）: %v", got)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("后置顶的应排最上：期望 [1, 2]，实际 %v", got)
	}
}

// rootOrder 返回当前公开列表的根评论 id 序列
func rootOrder(t *testing.T, svc *Service) []int64 {
	t.Helper()
	res, err := svc.ListComments("/test", "default", 1, 50, "asc")
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	ids := make([]int64, 0, len(res.Roots))
	for _, r := range res.Roots {
		ids = append(ids, r.ID)
	}
	return ids
}
