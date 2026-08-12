package service

import (
	"encoding/json"
	"testing"
)

// TestImportComments_PreservesAuditStatus 验证导入审核状态保留逻辑：
// - waline status（approved/waiting/spam）与原生格式 isAudited 原样保留；
// - 未解析到状态、状态未知或数值越界时回落默认已通过（1），避免产生不可见孤儿评论。
func TestImportComments_PreservesAuditStatus(t *testing.T) {
	cases := []struct {
		name   string
		source string
		// waline 专用：status 字段值
		wstatus string
		// 原生格式专用：isAudited 字段值（*int 区分"缺省"与"0"）
		nisAudited *int
		want       int
	}{
		{"waline approved", "waline", "approved", nil, 1},
		{"waline waiting", "waline", "waiting", nil, 0},
		{"waline pending", "waline", "pending", nil, 0},
		{"waline spam", "waline", "spam", nil, -1},
		{"waline 大写+空格状态", "waline", "  APPROVED ", nil, 1},
		{"waline 无状态字段默认已通过", "waline", "", nil, 1},
		{"waline 未知状态回落已通过", "waline", "unknown", nil, 1},
		{"原生 isAudited=0 保留待审", "dwxcomment", "", intp(0), 0},
		{"原生 isAudited=-1 保留垃圾", "dwxcomment", "", intp(-1), -1},
		{"原生 isAudited=5 越界回落已通过", "dwxcomment", "", intp(5), 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := testService(t)

			var rec map[string]interface{}
			if c.source == "waline" {
				rec = map[string]interface{}{
					"objectId":   "1",
					"comment":    map[string]interface{}{"nick": "tester", "mail": "", "content": "hello import", "url": "/test"},
					"insertedAt": "2026-01-01 10:00:00",
				}
				if c.wstatus != "" {
					rec["status"] = c.wstatus
				}
			} else {
				rec = map[string]interface{}{
					"pageId":  "/test",
					"site":    "default",
					"nick":    "tester",
					"content": "hello import",
				}
				if c.nisAudited != nil {
					rec["isAudited"] = *c.nisAudited
				}
			}

			data, err := json.Marshal([]map[string]interface{}{rec})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			res, err := svc.ImportComments(c.source, data)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if res.Imported != 1 || res.Skipped != 0 {
				t.Fatalf("应导入 1 条、跳过 0 条, got %+v", res)
			}
			var got int
			if err := svc.DB.QueryRow(`SELECT is_audited FROM comments`).Scan(&got); err != nil {
				t.Fatalf("查询 is_audited 失败: %v", err)
			}
			if got != c.want {
				t.Fatalf("is_audited = %d, want %d", got, c.want)
			}
		})
	}
}

// TestImportComments_Deduplicate 验证同 page_id + content 已存在的评论被跳过。
func TestImportComments_Deduplicate(t *testing.T) {
	svc := testService(t)
	rec := map[string]interface{}{
		"pageId":  "/dup",
		"site":    "default",
		"nick":    "tester",
		"content": "same content",
	}
	data, err := json.Marshal([]map[string]interface{}{rec, rec}) // 两条完全相同
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := svc.ImportComments("dwxcomment", data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Imported != 1 || res.Skipped != 1 {
		t.Fatalf("应导入 1 条、跳过 1 条（重复）, got %+v", res)
	}
}

// intp 返回 int 指针（测试辅助）
func intp(v int) *int { return &v }
