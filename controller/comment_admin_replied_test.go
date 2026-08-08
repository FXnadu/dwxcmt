package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// insertRawComment 直接插入一条指定属性的评论，用于构造 mock 数据
// （绕过业务层校验，灵活控制 is_audited / is_admin / parent_id 等字段）
func insertRawComment(t *testing.T, env *testEnv, pageID, site, nick, content string, parentID, rootID, isAudited, isAdmin int64) {
	t.Helper()
	now := time.Now().Unix()
	_, err := env.svc.DB.Exec(
		`INSERT INTO comments (page_id, site, nick, content, parent_id, root_id, is_audited, is_admin, create_time, update_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pageID, site, nick, content, parentID, rootID, isAudited, isAdmin, now, now,
	)
	if err != nil {
		t.Fatalf("插入 mock 评论失败: %v", err)
	}
}

// doReplied 带 JWT 调用 GET /api/v1/admin/comments/replied，返回解码后的统一响应
func doReplied(t *testing.T, env *testEnv, token, query string) utilsResponse {
	t.Helper()
	ctl := NewCommentAdmin(env.svc)
	h := env.auth.Middleware(http.HandlerFunc(ctl.Replied))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/comments/replied?"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp utilsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	return resp
}

// TestReplied_FiltersAdminReplies 构造混合 mock 数据，验证接口只返回后台回复（is_admin=1）
func TestReplied_FiltersAdminReplies(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	// mock 数据：
	// 1. 普通用户根评论（不应出现）
	root := insertComment(t, env, "/post")
	// 2. 普通用户回复（is_admin=0，不应出现）
	insertRawComment(t, env, "/post", "default", "user", "user reply", root, root, 1, 0)
	// 3. 站长通过后台接口回复（is_admin=1，应出现）
	id1, err := env.svc.ReplyComment(root, "站长回复一")
	if err != nil {
		t.Fatalf("构造站长回复失败: %v", err)
	}
	id2, err := env.svc.ReplyComment(root, "站长回复二")
	if err != nil {
		t.Fatalf("构造站长回复失败: %v", err)
	}
	// 4. 待审状态的站长评论（is_admin=1 且 is_audited=0，应出现：接口只按 is_admin 筛选）
	insertRawComment(t, env, "/post", "default", "站长", "待审站长评论", 0, 0, 0, 1)

	resp := doReplied(t, env, token, "page=1&pageSize=10&site=default")
	if resp.Code != 0 {
		t.Fatalf("调用 replied 接口失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		List []struct {
			ID        int64  `json:"id"`
			Nick      string `json:"nick"`
			Content   string `json:"content"`
			IsAdmin   int    `json:"isAdmin"`
			IsAudited int    `json:"isAudited"`
			Email     string `json:"email"`
			IP        string `json:"ip"`
		} `json:"list"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析列表响应失败: %v", err)
	}
	if data.Total != 3 {
		t.Fatalf("应只返回 3 条后台回复, got total=%d", data.Total)
	}
	gotIDs := map[int64]bool{}
	for _, it := range data.List {
		if it.IsAdmin != 1 {
			t.Fatalf("返回了非后台回复的评论: %+v", it)
		}
		gotIDs[it.ID] = true
	}
	for _, want := range []int64{id1, id2} {
		if !gotIDs[want] {
			t.Fatalf("缺少站长回复 id=%d, 返回的列表: %+v", want, data.List)
		}
	}
	// 站长视角可见邮箱/IP 等隐私字段
	if len(data.List) > 0 && data.List[0].Email == "" && data.List[0].IP == "" {
		t.Log("注意：当前返回的后台回复无邮箱/IP（站长回复本身不携带）")
	}
}

// TestReplied_KeywordFilter 验证关键词筛选（昵称 / 内容模糊匹配）
func TestReplied_KeywordFilter(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	root := insertComment(t, env, "/post")
	if _, err := env.svc.ReplyComment(root, "感谢支持"); err != nil {
		t.Fatalf("构造站长回复失败: %v", err)
	}
	if _, err := env.svc.ReplyComment(root, "欢迎再来"); err != nil {
		t.Fatalf("构造站长回复失败: %v", err)
	}

	// 按内容关键词
	resp := doReplied(t, env, token, "page=1&pageSize=10&site=default&keyword="+url.QueryEscape("感谢"))
	if resp.Code != 0 {
		t.Fatalf("关键词筛选失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		List []struct {
			Content string `json:"content"`
		} `json:"list"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.Total != 1 || len(data.List) != 1 || data.List[0].Content != "感谢支持" {
		t.Fatalf("关键词「感谢」应命中 1 条, got total=%d list=%+v", data.Total, data.List)
	}

	// 无匹配关键词 → 空列表
	resp = doReplied(t, env, token, "page=1&pageSize=10&site=default&keyword="+url.QueryEscape("不存在的词"))
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.Total != 0 || len(data.List) != 0 {
		t.Fatalf("无匹配关键词应返回空列表, got total=%d", data.Total)
	}
}

// TestReplied_Pagination 验证分页
func TestReplied_Pagination(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	root := insertComment(t, env, "/post")
	for i := 0; i < 5; i++ {
		if _, err := env.svc.ReplyComment(root, fmt.Sprintf("回复%d", i)); err != nil {
			t.Fatalf("构造站长回复失败: %v", err)
		}
	}

	resp := doReplied(t, env, token, "page=1&pageSize=2&site=default")
	if resp.Code != 0 {
		t.Fatalf("分页请求失败: code=%d", resp.Code)
	}
	var data struct {
		List       []struct{ ID int64 } `json:"list"`
		Total      int                  `json:"total"`
		Page       int                  `json:"page"`
		PageSize   int                  `json:"pageSize"`
		TotalPages int                  `json:"totalPages"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.Total != 5 || len(data.List) != 2 || data.TotalPages != 3 {
		t.Fatalf("分页结果不正确: total=%d len=%d totalPages=%d", data.Total, len(data.List), data.TotalPages)
	}
}
