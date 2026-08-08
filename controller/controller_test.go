package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dwxcmt/config"
	"dwxcmt/middleware"
	"dwxcmt/model"
	"dwxcmt/pkg/cache"
	"dwxcmt/service"
)

// testEnv controller 测试环境：svc + auth + 清理函数
type testEnv struct {
	svc       *service.Service
	auth      *middleware.Auth
	blacklist *middleware.TokenBlacklist
	cleanup   func()
}

// newTestEnv 创建临时 SQLite DB 与完整业务层，测试结束后自动清理
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("lc-ctl-test-%d.db", time.Now().UnixNano()))
	db, err := model.Open(tmpFile)
	if err != nil {
		t.Fatalf("打开测试 DB 失败: %v", err)
	}
	cleanup := func() {
		db.Close()
		os.Remove(tmpFile)
		os.Remove(tmpFile + "-wal")
		os.Remove(tmpFile + "-shm")
	}
	cfg := config.Default()
	cfg.Auth.JWTSecret = "ctl-test-secret-0123456789abcdef0123456789"
	c := cache.New(64, time.Minute)
	svc := service.New(db, cfg, c)

	blacklist := middleware.NewTokenBlacklist()
	auth := &middleware.Auth{JWT: svc.JWT, Blacklist: blacklist}
	env := &testEnv{svc: svc, auth: auth, blacklist: blacklist, cleanup: cleanup}
	t.Cleanup(func() {
		blacklist.Stop()
		cleanup()
	})
	return env
}

// doJSON 执行 HTTP handler，返回解码后的统一响应
func doJSON(t *testing.T, h http.Handler, method, body string, token string) (int, utilsResponse) {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, "/", reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp utilsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, body=%s", err, rec.Body.String())
	}
	return rec.Code, resp
}

type utilsResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// registerAndLogin 注册首个管理员并登录，返回 JWT
func registerAndLogin(t *testing.T, env *testEnv) string {
	t.Helper()
	reg := NewAdmin(env.svc, env.auth)
	if _, resp := doJSON(t, http.HandlerFunc(reg.Register), http.MethodPost, `{"username":"admin","password":"Admin123456"}`, ""); resp.Code != 0 {
		t.Fatalf("注册失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	login := NewAdmin(env.svc, env.auth)
	_, resp := doJSON(t, http.HandlerFunc(login.Login), http.MethodPost, `{"username":"admin","password":"Admin123456"}`, "")
	if resp.Code != 0 {
		t.Fatalf("登录失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil || data.Token == "" {
		t.Fatalf("登录响应缺少 token: %s", resp.Data)
	}
	return data.Token
}

// insertComment 直接插入一条已审核根评论，返回 id
func insertComment(t *testing.T, env *testEnv, pageID string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := env.svc.DB.Exec(
		`INSERT INTO comments (page_id, site, nick, content, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES (?, 'default', 'tester', 'hello', 0, 0, 1, ?, ?)`,
		pageID, now, now,
	)
	if err != nil {
		t.Fatalf("插入测试评论失败: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("读取评论 id 失败: %v", err)
	}
	return id
}

// ===== 公开接口 =====

func TestHealth(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	status, resp := doJSON(t, http.HandlerFunc(ctl.Health), http.MethodGet, "", "")
	if status != http.StatusOK || resp.Code != 0 {
		t.Fatalf("health 应返回 200/0, got status=%d code=%d", status, resp.Code)
	}
}

func TestList_MissingPageID(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	_, resp := doJSON(t, http.HandlerFunc(ctl.List), http.MethodGet, "", "")
	if resp.Code != 1006 {
		t.Fatalf("缺少 pageId 应返回 1006, got %d", resp.Code)
	}
}

func TestSubmit_ValidationErrors(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"缺少 pageId", `{"nick":"a","content":"hello"}`, 1006},
		{"昵称超长", `{"pageId":"p1","nick":"` + `aaaaaaaaaaaaaaaaaaaaa` + `","content":"hello"}`, 1003},
		{"内容为空", `{"pageId":"p1","nick":"a","content":""}`, 1002},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, resp := doJSON(t, http.HandlerFunc(ctl.Submit), http.MethodPost, c.body, "")
			if resp.Code != c.want {
				t.Fatalf("期望 code=%d, got %d (%s)", c.want, resp.Code, resp.Msg)
			}
		})
	}
}

func TestSubmit_OK(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	_, resp := doJSON(t, http.HandlerFunc(ctl.Submit), http.MethodPost,
		`{"pageId":"p1","nick":"tester","content":"hello world"}`, "")
	if resp.Code != 0 {
		t.Fatalf("提交应成功, code=%d msg=%s", resp.Code, resp.Msg)
	}
	var data struct {
		ID      int64 `json:"id"`
		Audited bool  `json:"audited"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if data.ID == 0 || data.Audited {
		t.Fatalf("新评论应待审且返回 id, got %+v", data)
	}
}

func TestLike_NotFound(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comment/99999/like", nil)
	req.SetPathValue("id", "99999")
	rec := httptest.NewRecorder()
	ctl.Like(rec, req)
	var resp utilsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != 3001 {
		t.Fatalf("不存在的评论点赞应返回 3001, got %d", resp.Code)
	}
}

func TestLike_OK(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	id := insertComment(t, env, "/test")

	// 第一次点赞 → likeCount=1
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/comment/%d/like", id), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", id))
	rec := httptest.NewRecorder()
	ctl.Like(rec, req)
	var first utilsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil || first.Code != 0 {
		t.Fatalf("首次点赞失败: %+v", first)
	}
	var d1 struct {
		LikeCount int `json:"likeCount"`
	}
	_ = json.Unmarshal(first.Data, &d1)
	if d1.LikeCount != 1 {
		t.Fatalf("首次点赞 likeCount 应为 1, got %d", d1.LikeCount)
	}

	// 窗口内重复点赞 → 幂等，仍为 1
	rec2 := httptest.NewRecorder()
	ctl.Like(rec2, req)
	var second utilsResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &second)
	var d2 struct {
		LikeCount int `json:"likeCount"`
	}
	_ = json.Unmarshal(second.Data, &d2)
	if d2.LikeCount != 1 {
		t.Fatalf("窗口内重复点赞应幂等返回 1, got %d", d2.LikeCount)
	}
}

// ===== 管理接口：注册 / 登录 / 鉴权 =====

func TestRegister_FirstOwner_SecondPending(t *testing.T) {
	env := newTestEnv(t)
	reg := NewAdmin(env.svc, env.auth)

	// 首个注册 → 站长，可直接登录
	_, resp1 := doJSON(t, http.HandlerFunc(reg.Register), http.MethodPost, `{"username":"admin","password":"Admin123456"}`, "")
	if resp1.Code != 0 {
		t.Fatalf("首个注册应成功, code=%d", resp1.Code)
	}
	// 后续注册 → 允许注册，但进入待审批，暂不可登录
	_, resp2 := doJSON(t, http.HandlerFunc(reg.Register), http.MethodPost, `{"username":"admin2","password":"Admin123456"}`, "")
	if resp2.Code != 0 {
		t.Fatalf("后续注册应成功（待审批）, code=%d msg=%s", resp2.Code, resp2.Msg)
	}
	var owner, pending int
	if err := env.svc.DB.QueryRow(`SELECT COUNT(*) FROM admins WHERE is_owner = 1`).Scan(&owner); err != nil {
		t.Fatalf("查询站长数量失败: %v", err)
	}
	if err := env.svc.DB.QueryRow(`SELECT COUNT(*) FROM admins WHERE is_approved = 0`).Scan(&pending); err != nil {
		t.Fatalf("查询待审批数量失败: %v", err)
	}
	if owner != 1 {
		t.Fatalf("站长数量应为 1, got %d", owner)
	}
	if pending != 1 {
		t.Fatalf("待审批账号数量应为 1, got %d", pending)
	}

	// 站长可登录；待审批账号登录被拒（7010）
	login := NewAdmin(env.svc, env.auth)
	_, ok := doJSON(t, http.HandlerFunc(login.Login), http.MethodPost, `{"username":"admin","password":"Admin123456"}`, "")
	if ok.Code != 0 {
		t.Fatalf("站长应可登录, code=%d", ok.Code)
	}
	_, blocked := doJSON(t, http.HandlerFunc(login.Login), http.MethodPost, `{"username":"admin2","password":"Admin123456"}`, "")
	if blocked.Code != 7010 {
		t.Fatalf("待审批账号登录应返回 7010, got %d", blocked.Code)
	}
}

func TestRegister_Concurrent_SingleOwner(t *testing.T) {
	env := newTestEnv(t)
	reg := NewAdmin(env.svc, env.auth)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"username":"u%d","password":"Admin123456"}`, i)
			_, resp := doJSON(t, http.HandlerFunc(reg.Register), http.MethodPost, body, "")
			codes[i] = resp.Code
		}(i)
	}
	wg.Wait()

	for _, c := range codes {
		if c != 0 {
			t.Fatalf("并发注册应全部成功（多管理员模式）, got code=%d", c)
		}
	}
	// 并发注册下恰好只有 1 个站长，其余为待审批
	var owners, total int
	if err := env.svc.DB.QueryRow(`SELECT COUNT(*) FROM admins WHERE is_owner = 1`).Scan(&owners); err != nil {
		t.Fatalf("查询站长数量失败: %v", err)
	}
	if err := env.svc.DB.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&total); err != nil {
		t.Fatalf("查询管理员数量失败: %v", err)
	}
	if owners != 1 {
		t.Fatalf("并发注册站长数量应恰好为 1, got %d", owners)
	}
	if total != n {
		t.Fatalf("DB 中管理员数量应为 %d, got %d", n, total)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	env := newTestEnv(t)
	reg := NewAdmin(env.svc, env.auth)
	_, _ = doJSON(t, http.HandlerFunc(reg.Register), http.MethodPost, `{"username":"admin","password":"Admin123456"}`, "")

	login := NewAdmin(env.svc, env.auth)
	_, resp := doJSON(t, http.HandlerFunc(login.Login), http.MethodPost, `{"username":"admin","password":"WrongPass1"}`, "")
	if resp.Code != 3002 {
		t.Fatalf("错误密码应返回 3002, got %d", resp.Code)
	}
}

func TestAdminAPI_RequiresToken(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewCommentAdmin(env.svc)
	h := env.auth.Middleware(http.HandlerFunc(ctl.List))

	// 无 token → 3004
	_, resp := doJSON(t, h, http.MethodGet, "", "")
	if resp.Code != 3004 {
		t.Fatalf("无 token 应返回 3004, got %d", resp.Code)
	}

	// 伪造 token → 3004
	_, resp = doJSON(t, h, http.MethodGet, "", "invalid.token.here")
	if resp.Code != 3004 {
		t.Fatalf("伪造 token 应返回 3004, got %d", resp.Code)
	}
}

func TestAudit_WithToken(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	id := insertComment(t, env, "/test")

	audit := NewAdmin(env.svc, env.auth)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", fmt.Sprintf("%d", id))
		audit.Audit(w, r)
	}))
	_, resp := doJSON(t, h, http.MethodPut, `{"status":1}`, token)
	if resp.Code != 0 {
		t.Fatalf("审核应成功, code=%d msg=%s", resp.Code, resp.Msg)
	}

	// 审核后公开列表应能查到该评论
	commentCtl := NewComment(env.svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments?pageId=/test&pageSize=10", nil)
	rec := httptest.NewRecorder()
	commentCtl.List(rec, req)
	var listResp utilsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Code != 0 {
		t.Fatalf("公开列表失败: %+v", listResp)
	}
}

// ===== 站点配置 =====

func TestSettings_UpdateAuditMode_Rejected(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	ctl := NewSettings(env.svc)
	h := env.auth.Middleware(http.HandlerFunc(ctl.Update))

	_, resp := doJSON(t, h, http.MethodPut, `{"auditMode":"manual"}`, token)
	if resp.Code != 1001 {
		t.Fatalf("修改 auditMode 应拒绝 1001, got %d", resp.Code)
	}
}

func TestSettings_UpdateAndGet(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	ctl := NewSettings(env.svc)
	upd := env.auth.Middleware(http.HandlerFunc(ctl.Update))
	get := env.auth.Middleware(http.HandlerFunc(ctl.Get))

	_, resp := doJSON(t, upd, http.MethodPut, `{"siteName":"我的博客","notifyReply":false}`, token)
	if resp.Code != 0 {
		t.Fatalf("更新设置失败: code=%d msg=%s", resp.Code, resp.Msg)
	}
	_, resp = doJSON(t, get, http.MethodGet, "", token)
	if resp.Code != 0 {
		t.Fatalf("读取设置失败: code=%d", resp.Code)
	}
	var data struct {
		SiteName    string `json:"siteName"`
		NotifyReply bool   `json:"notifyReply"`
		AuditMode   string `json:"auditMode"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("解析设置响应失败: %v", err)
	}
	if data.SiteName != "我的博客" || data.NotifyReply || data.AuditMode != "all" {
		t.Fatalf("设置未生效或 auditMode 被改动: %+v", data)
	}
}

// ===== 置顶 =====

func TestPin_AndMaxLimit(t *testing.T) {
	env := newTestEnv(t)
	token := registerAndLogin(t, env)
	ctl := NewPin(env.svc)
	h := env.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctl.Pin(w, r)
	}))

	// 最多置顶 MaxPinnedPerPage 条（默认 3）
	max := env.svc.Cfg.Comment.MaxPinnedPerPage
	for i := 0; i < max; i++ {
		id := insertComment(t, env, "/page")
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/admin/comment/%d/pin", id), nil)
		req.SetPathValue("id", fmt.Sprintf("%d", id))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var resp utilsResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Code != 0 {
			t.Fatalf("第 %d 条置顶应成功, code=%d msg=%s", i+1, resp.Code, resp.Msg)
		}
	}

	// 第 4 条 → 拒绝
	id4 := insertComment(t, env, "/page")
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/admin/comment/%d/pin", id4), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", id4))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp utilsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != 1001 {
		t.Fatalf("超过置顶上限应拒绝 1001, got %d", resp.Code)
	}
}
