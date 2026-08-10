package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"dwxcmt/pkg/utils"
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

// insertRawCommentAt 插入一条指定 create_time / like_count 的已审核评论，用于构造排序测试数据
func insertRawCommentAt(t *testing.T, env *testEnv, pageID, nick, content string, createTime, likeCount int64) {
	t.Helper()
	_, err := env.svc.DB.Exec(
		`INSERT INTO comments (page_id, site, nick, content, parent_id, root_id, is_audited, like_count, create_time, update_time)
		 VALUES (?, 'default', ?, ?, 0, 0, 1, ?, ?, ?)`,
		pageID, nick, content, likeCount, createTime, createTime,
	)
	if err != nil {
		t.Fatalf("插入 mock 评论失败: %v", err)
	}
}

// doList 带 JWT 调用 GET /api/v1/admin/comments，返回解码后的统一响应
func doList(t *testing.T, env *testEnv, token, query string) utilsResponse {
	t.Helper()
	ctl := NewCommentAdmin(env.svc)
	h := env.auth.Middleware(http.HandlerFunc(ctl.List))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/comments?"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp utilsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	return resp
}

// TestList_Sort 验证管理端列表排序：newest / oldest / hot 与非法值拒绝
func TestList_Sort(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	now := time.Now().Unix()
	// A：最早（now-300，like=1）；B：中间（now-200，like=2）；C：最新（now-100，like=5）
	insertRawCommentAt(t, env, "/post", "A", "最早的评论", now-300, 1)
	insertRawCommentAt(t, env, "/post", "B", "中间的评论", now-200, 2)
	insertRawCommentAt(t, env, "/post", "C", "最新的评论", now-100, 5)

	// 按昵称断言排序：newest 最新在前
	nickOrder := func(query string) []string {
		t.Helper()
		resp := doList(t, env, token, query)
		if resp.Code != 0 {
			t.Fatalf("列表请求失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		var data struct {
			List []struct {
				Nick string `json:"nick"`
			} `json:"list"`
		}
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		got := make([]string, 0, len(data.List))
		for _, it := range data.List {
			got = append(got, it.Nick)
		}
		return got
	}

	if got := nickOrder("page=1&pageSize=10&site=default&sort=newest"); !reflect.DeepEqual(got, []string{"C", "B", "A"}) {
		t.Fatalf("newest 排序应为 [C B A], got %v", got)
	}
	if got := nickOrder("page=1&pageSize=10&site=default&sort=oldest"); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("oldest 排序应为 [A B C], got %v", got)
	}
	if got := nickOrder("page=1&pageSize=10&site=default&sort=hot"); !reflect.DeepEqual(got, []string{"C", "B", "A"}) {
		t.Fatalf("hot 排序应为 [C B A], got %v", got)
	}

	// 缺省 sort 等价 newest
	if got := nickOrder("page=1&pageSize=10&site=default"); !reflect.DeepEqual(got, []string{"C", "B", "A"}) {
		t.Fatalf("缺省排序应等同 newest [C B A], got %v", got)
	}

	// 非法 sort 值应拒绝
	if resp := doList(t, env, token, "page=1&pageSize=10&site=default&sort=DROP"); resp.Code != utils.CodeErrInvalidParam {
		t.Fatalf("非法 sort 应返回 CodeErrInvalidParam(%d), got code=%d", utils.CodeErrInvalidParam, resp.Code)
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

// TestList_PassedSearch_NoSpamMix 验证「已通过」Tab（status=1）中搜索关键词时，
// 即使垃圾（is_audited=-1）与待审核（is_audited=0）评论命中相同关键词，也不会混入结果
func TestList_PassedSearch_NoSpamMix(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	// 三条评论命中相同关键词「内部测试」，状态各不相同
	insertRawComment(t, env, "/post", "default", "正常用户", "这是内部测试的已通过评论", 0, 0, 1, 0)  // 已通过
	insertRawComment(t, env, "/post", "default", "垃圾用户", "这是内部测试的垃圾评论", 0, 0, -1, 0)  // 垃圾
	insertRawComment(t, env, "/post", "default", "待审用户", "这是内部测试的待审评论", 0, 0, 0, 0)   // 待审核

	resp := doList(t, env, token, "page=1&pageSize=10&site=default&status=1&keyword="+url.QueryEscape("内部测试"))
	if resp.Code != 0 {
		t.Fatalf("已通过列表搜索失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		List []struct {
			Nick      string `json:"nick"`
			IsAudited int    `json:"isAudited"`
		} `json:"list"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.Total != 1 {
		t.Fatalf("已通过搜索应只命中 1 条（垃圾/待审核不得混入）, got total=%d list=%+v", data.Total, data.List)
	}
	if len(data.List) != 1 || data.List[0].Nick != "正常用户" {
		t.Fatalf("应只返回已通过评论「正常用户」, got %+v", data.List)
	}
	if data.List[0].IsAudited != 1 {
		t.Fatalf("返回的评论 isAudited 应为 1, got %d", data.List[0].IsAudited)
	}
}

// TestList_SpamSearch_NoMix 验证「垃圾评论」Tab（status=-1）中搜索关键词时，
// 即使已通过（is_audited=1）与待审核（is_audited=0）评论命中相同关键词，也不会混入结果
func TestList_SpamSearch_NoMix(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)

	// 三条评论命中相同关键词「推广」，状态各不相同
	insertRawComment(t, env, "/post", "default", "广告用户", "这是推广的垃圾评论", 0, 0, -1, 0) // 垃圾
	insertRawComment(t, env, "/post", "default", "正常用户", "这是推广的正常评论", 0, 0, 1, 0)  // 已通过
	insertRawComment(t, env, "/post", "default", "待审用户", "这是推广的待审评论", 0, 0, 0, 0)  // 待审核

	resp := doList(t, env, token, "page=1&pageSize=10&site=default&status=-1&keyword="+url.QueryEscape("推广"))
	if resp.Code != 0 {
		t.Fatalf("垃圾评论列表搜索失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		List []struct {
			Nick      string `json:"nick"`
			IsAudited int    `json:"isAudited"`
		} `json:"list"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.Total != 1 {
		t.Fatalf("垃圾评论搜索应只命中 1 条（已通过/待审核不得混入）, got total=%d list=%+v", data.Total, data.List)
	}
	if len(data.List) != 1 || data.List[0].Nick != "广告用户" {
		t.Fatalf("应只返回垃圾评论「广告用户」, got %+v", data.List)
	}
	if data.List[0].IsAudited != -1 {
		t.Fatalf("返回的评论 isAudited 应为 -1, got %d", data.List[0].IsAudited)
	}
}
