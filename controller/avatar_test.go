package controller

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doAvatarReq 构造 GET /api/v1/avatars/{id} 请求并执行 Avatar 处理器
func doAvatarReq(ctl *CommentController, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	ctl.Avatar(rec, req)
	return rec
}

// TestAvatarHTTP_InvalidID 非法评论 ID → 404（参数错误不进入服务层）
func TestAvatarHTTP_InvalidID(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)

	for _, id := range []string{"abc", "-1", "1.5", ""} {
		rec := doAvatarReq(ctl, id)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("非法 id=%q 应返回 404, got %d", id, rec.Code)
		}
	}
}

// TestAvatarHTTP_MissingComment 评论不存在 → 404（model.ErrNotFound 透传）
func TestAvatarHTTP_MissingComment(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)

	rec := doAvatarReq(ctl, "99999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在评论应返回 404, got %d", rec.Code)
	}
}

// TestAvatarHTTP_NoEmail 无邮箱评论 → 404（前端回退字母头像）
func TestAvatarHTTP_NoEmail(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	id := insertComment(t, env, "/test")

	rec := doAvatarReq(ctl, fmt.Sprintf("%d", id))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无邮箱评论应返回 404, got %d", rec.Code)
	}
}

// TestAvatarHTTP_CacheHit_SuccessHeaders 磁盘缓存命中 → 200：
// 验证成功路径的 Content-Type / Cache-Control / X-Content-Type-Options 响应头。
// 通过预置新鲜缓存文件命中（不触发外网回源）。
func TestAvatarHTTP_CacheHit_SuccessHeaders(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)

	// 插入带邮箱评论（与公开字段脱敏无关，直接 SQL）
	email := "cachehit@example.com"
	now := time.Now().Unix()
	res, err := env.svc.DB.Exec(
		`INSERT INTO comments (page_id, site, nick, email, content, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES ('/test', 'default', 'tester', ?, 'hello', 0, 0, 1, ?, ?)`,
		email, now, now,
	)
	if err != nil {
		t.Fatalf("插入带邮箱测试评论失败: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("读取评论 id 失败: %v", err)
	}

	// 预置新鲜磁盘缓存（缓存目录相对测试 cwd 为 ./data/avatars）
	hash := md5.Sum([]byte(email))
	dir := filepath.Join("data", "avatars")
	cachePath := filepath.Join(dir, hex.EncodeToString(hash[:])+".jpg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建缓存目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) // 仅清理本测试产生的目录
	if err := os.WriteFile(cachePath, []byte("cache-hit-img"), 0o644); err != nil {
		t.Fatalf("写预置缓存失败: %v", err)
	}

	rec := doAvatarReq(ctl, fmt.Sprintf("%d", id))
	if rec.Code != http.StatusOK {
		t.Fatalf("缓存命中应返回 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "cache-hit-img" {
		t.Fatalf("应返回缓存图片内容, got %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type 应为 image/jpeg, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=86400") {
		t.Fatalf("Cache-Control 应含 public max-age=86400, got %q", cc)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("应设置 nosniff, got %q", nosniff)
	}
}

// TestAvatarHTTP_DBFailure_InternalError 数据库等真实故障 → 500（而非 404）：
// 这是 404/500 语义修复的关键断言——真实故障不得静默降级为「该用户无头像」。
func TestAvatarHTTP_DBFailure_InternalError(t *testing.T) {
	env := newTestEnv(t)
	ctl := NewComment(env.svc)
	id := insertComment(t, env, "/test")

	// 关闭 DB 模拟服务层真实故障（GetComment 透传 DB 错误）
	if err := env.svc.DB.Close(); err != nil {
		t.Fatalf("关闭测试 DB 失败: %v", err)
	}

	rec := doAvatarReq(ctl, fmt.Sprintf("%d", id))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DB 故障应返回 500, got %d", rec.Code)
	}
}
