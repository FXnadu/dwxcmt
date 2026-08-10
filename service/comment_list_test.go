package service

import (
	"fmt"
	"reflect"
	"testing"
)

// TestListAllComments_Pagination_AllTied 极端并列场景：25 条评论「同一秒创建 + 点赞数全为 0」。
// 在 newest / oldest / hot 三种排序下，create_time 与 like_count 全部并列，仅靠末尾 id 兜底区分。
// 校验：
//  1. 分页遍历拼接出的有序 id 序列与单次全量查询完全一致（跨页不重复、不漏项、顺序稳定）；
//  2. id 兜底方向确定：newest → id 降序；oldest → id 升序；hot（同赞同秒）→ id 降序。
func TestListAllComments_Pagination_AllTied(t *testing.T) {
	svc := testService(t)

	const n = 25
	const sameSec = 1000 // 全部评论同一秒创建
	// id 从 101 起连续递增，确保 id 顺序可预测
	for i := int64(0); i < n; i++ {
		id := 101 + i
		if _, err := svc.DB.Exec(
			`INSERT INTO comments (id, page_id, site, nick, content, parent_id, root_id, is_audited, like_count, create_time, update_time)
			 VALUES (?, '/test', 'default', ?, ?, 0, 0, 1, 0, ?, ?)`,
			id, fmt.Sprintf("user%d", i), fmt.Sprintf("content%d", i), sameSec, sameSec,
		); err != nil {
			t.Fatalf("插入评论 id=%d 失败: %v", id, err)
		}
	}

	const pageSize = 7 // 非整除，强制跨页（25/7 → 4 页）
	cases := []struct {
		name     string
		sort     string
		firstID  int64 // id 兜底方向断言
		lastID   int64
	}{
		{"newest", "newest", 101 + n - 1, 101},
		{"oldest", "oldest", 101, 101 + n - 1},
		{"hot", "hot", 101 + n - 1, 101},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 全量：单次取全部（pageSize 传 n，避免被 ListAllComments 的 100 上限 clamp）
			all, total, err := svc.ListAllComments(1, n, nil, "", "default", tc.sort)
			if err != nil {
				t.Fatalf("全量查询失败: %v", err)
			}
			if total != n || len(all) != n {
				t.Fatalf("全量应返回 %d 条, total=%d len=%d", n, total, len(all))
			}

			// 分页遍历拼接有序 id 序列
			var paged []int64
			for page := 1; ; page++ {
				list, _, err := svc.ListAllComments(page, pageSize, nil, "", "default", tc.sort)
				if err != nil {
					t.Fatalf("分页查询 page=%d 失败: %v", page, err)
				}
				for _, c := range list {
					paged = append(paged, c.ID)
				}
				if page*pageSize >= total {
					break
				}
			}

			if len(paged) != n {
				t.Fatalf("分页拼接应为 %d 条, got %d（存在重复或漏项）: %v", n, len(paged), paged)
			}
			seen := make(map[int64]bool, n)
			for _, id := range paged {
				if seen[id] {
					t.Fatalf("分页拼接出现重复 id=%d: %v", id, paged)
				}
				seen[id] = true
			}

			var allIDs []int64
			for _, c := range all {
				allIDs = append(allIDs, c.ID)
			}
			if !reflect.DeepEqual(paged, allIDs) {
				t.Fatalf("分页拼接与全量顺序不一致（跨页重复/漏项）\n分页: %v\n全量: %v", paged, allIDs)
			}
			if allIDs[0] != tc.firstID || allIDs[n-1] != tc.lastID {
				t.Fatalf("%s 排序下 id 兜底方向错误: 期望首=%d 尾=%d, got 首=%d 尾=%d (%v)",
					tc.name, tc.firstID, tc.lastID, allIDs[0], allIDs[n-1], allIDs)
			}
		})
	}
}

// TestListAllComments_Pagination_Mixed 混合场景：不同时间 + 不同点赞 + 部分并列。
// 验证各排序的语义正确，且分页拼接与全量结果一致（无重复、无漏项）。
// 数据（id 即插入顺序）：
//   A(1): create_time=3000, like=5
//   B(2): create_time=2000, like=3
//   C(3): create_time=1000, like=9
//   D(4): create_time=1000, like=9   ← 与 C 同秒同赞，靠 id 兜底
//   E(5): create_time=1000, like=1
//
// 期望顺序：
//   newest: [1, 2, 5, 4, 3]  （时间倒序；同秒组 C/D/E 按 id 倒序，like_count 不影响）
//   oldest: [3, 4, 5, 2, 1]  （时间正序；同秒组按 id 正序）
//   hot:    [4, 3, 1, 2, 5]  （点赞倒序；同赞同秒按 id 倒序）
func TestListAllComments_Pagination_Mixed(t *testing.T) {
	svc := testService(t)

	type seed struct {
		id     int64
		ctime  int64
		likes  int
	}
	seeds := []seed{
		{1, 3000, 5},
		{2, 2000, 3},
		{3, 1000, 9},
		{4, 1000, 9},
		{5, 1000, 1},
	}
	for _, s := range seeds {
		if _, err := svc.DB.Exec(
			`INSERT INTO comments (id, page_id, site, nick, content, parent_id, root_id, is_audited, like_count, create_time, update_time)
			 VALUES (?, '/test', 'default', ?, ?, 0, 0, 1, ?, ?, ?)`,
			s.id, fmt.Sprintf("user%d", s.id), fmt.Sprintf("content%d", s.id), s.likes, s.ctime, s.ctime,
		); err != nil {
			t.Fatalf("插入评论 id=%d 失败: %v", s.id, err)
		}
	}

	cases := []struct {
		name string
		sort string
		want []int64
	}{
		{"newest", "newest", []int64{1, 2, 5, 4, 3}},
		{"oldest", "oldest", []int64{3, 4, 5, 2, 1}},
		{"hot", "hot", []int64{4, 3, 1, 2, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 全量
			all, total, err := svc.ListAllComments(1, 50, nil, "", "default", tc.sort)
			if err != nil {
				t.Fatalf("全量查询失败: %v", err)
			}
			var allIDs []int64
			for _, c := range all {
				allIDs = append(allIDs, c.ID)
			}
			if !reflect.DeepEqual(allIDs, tc.want) {
				t.Fatalf("%s 排序语义错误: got %v, want %v", tc.name, allIDs, tc.want)
			}
			if total != len(seeds) {
				t.Fatalf("total 应为 %d, got %d", len(seeds), total)
			}

			// 分页遍历（pageSize=3，5/3 → 2 页），拼接后与全量一致
			const pageSize = 3
			var paged []int64
			for page := 1; ; page++ {
				list, _, err := svc.ListAllComments(page, pageSize, nil, "", "default", tc.sort)
				if err != nil {
					t.Fatalf("分页查询 page=%d 失败: %v", page, err)
				}
				for _, c := range list {
					paged = append(paged, c.ID)
				}
				if page*pageSize >= total {
					break
				}
			}
			if !reflect.DeepEqual(paged, allIDs) {
				t.Fatalf("%s 分页拼接与全量不一致（重复/漏项）: 分页 %v, 全量 %v", tc.name, paged, allIDs)
			}
		})
	}
}
