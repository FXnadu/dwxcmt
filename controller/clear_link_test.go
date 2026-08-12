package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// insertCommentWithLink 直接插入一条已审核且带网站链接的根评论，返回 id
func insertCommentWithLink(t *testing.T, env *testEnv, pageID, link string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := env.svc.DB.Exec(
		`INSERT INTO comments (page_id, site, nick, content, link, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES (?, 'default', 'tester', 'hello', ?, 0, 0, 1, ?, ?)`,
		pageID, link, now, now,
	)
	if err != nil {
		t.Fatalf("插入带链接评论失败: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("读取评论 id 失败: %v", err)
	}
	return id
}

// listLinks 调用公开列表接口，返回所有根评论的 link 字段
func listLinks(t *testing.T, env *testEnv, pageID string) []string {
	t.Helper()
	ctl := NewComment(env.svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments?pageId="+pageID+"&pageSize=10", nil)
	rec := httptest.NewRecorder()
	ctl.List(rec, req)
	var resp utilsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("公开列表响应不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("公开列表失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		Roots []struct {
			ID   int64  `json:"id"`
			Link string `json:"link"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析列表数据失败: %v", err)
	}
	links := make([]string, 0, len(data.Roots))
	for _, r := range data.Roots {
		links = append(links, r.Link)
	}
	return links
}

// TestClearLink_HTTP 端到端验证「去除链接」：
// 公开列表先能看到链接 → 管理员调用去除链接 → 链接从公开列表消失、评论本身保留。
func TestClearLink_HTTP(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	pageID := "/mock-page"

	// 1. 构造 mock 数据：一条已审核、带外链的评论
	id := insertCommentWithLink(t, env, pageID, "https://spam.example.com")

	// 2. 清空缓存后首次加载公开列表：链接应可见
	first := listLinks(t, env, pageID)
	if len(first) != 1 || first[0] != "https://spam.example.com" {
		t.Fatalf("去除前链接应可见, got %v", first)
	}

	// 3. 管理员调用去除链接接口（带 JWT，设置路径参数 id）
	ctl := NewAdmin(env.svc, env.auth)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", fmt.Sprintf("%d", id))
		ctl.ClearLink(w, r)
	}))
	status, resp := doJSON(t, h, http.MethodPut, "", token)
	if status != http.StatusOK || resp.Code != 0 {
		t.Fatalf("去除链接应成功, status=%d code=%d msg=%s", status, resp.Code, resp.Msg)
	}
	var d struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(resp.Data, &d)
	if d.ID != id {
		t.Fatalf("响应应返回评论 id %d, got %d", id, d.ID)
	}

	// 4. DB 中 link 已清空，评论本身保留
	var link string
	if err := env.svc.DB.QueryRow(`SELECT link FROM comments WHERE id = ?`, id).Scan(&link); err != nil {
		t.Fatalf("查询评论失败: %v", err)
	}
	if link != "" {
		t.Errorf("DB 中 link 应已清空, got %q", link)
	}
	var cnt int
	if err := env.svc.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE id = ?`, id).Scan(&cnt); err != nil {
		t.Fatalf("查询评论数量失败: %v", err)
	}
	if cnt != 1 {
		t.Errorf("评论本身应保留, got count=%d", cnt)
	}

	// 5. 二次加载公开列表：缓存已失效，链接消失
	second := listLinks(t, env, pageID)
	if len(second) != 1 || second[0] != "" {
		t.Fatalf("去除后列表链接应为空, got %v", second)
	}
}

// TestClearLink_RequiresToken 去除链接必须带 JWT
func TestClearLink_RequiresToken(t *testing.T) {
	env := newTestEnv(t)
	id := insertCommentWithLink(t, env, "/mock-page", "https://spam.example.com")
	ctl := NewAdmin(env.svc, env.auth)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", fmt.Sprintf("%d", id))
		ctl.ClearLink(w, r)
	}))
	_, resp := doJSON(t, h, http.MethodPut, "", "")
	if resp.Code != 3004 {
		t.Fatalf("无 token 应返回 3004, got %d", resp.Code)
	}
}

// TestClearLink_NotFound 不存在的评论返回 3001
func TestClearLink_NotFound(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	ctl := NewAdmin(env.svc, env.auth)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "99999")
		ctl.ClearLink(w, r)
	}))
	_, resp := doJSON(t, h, http.MethodPut, "", token)
	if resp.Code != 3001 {
		t.Fatalf("不存在的评论应返回 3001, got %d", resp.Code)
	}
}

// TestClearLink_InvalidID 非法 id 参数返回 1001
func TestClearLink_InvalidID(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	ctl := NewAdmin(env.svc, env.auth)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "abc")
		ctl.ClearLink(w, r)
	}))
	_, resp := doJSON(t, h, http.MethodPut, "", token)
	if resp.Code != 1001 {
		t.Fatalf("非法 id 应返回 1001, got %d", resp.Code)
	}
}
