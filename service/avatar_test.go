package service

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dwxcmt/model"
)

// withTempAvatarDir 把头像缓存目录指到临时目录，并在测试结束时恢复
func withTempAvatarDir(t *testing.T) {
	t.Helper()
	orig := avatarDir
	avatarDir = t.TempDir()
	t.Cleanup(func() { avatarDir = orig })
}

// insertTestCommentWithEmail 插入一条带邮箱的评论（Avatar 链路测试用）
func insertTestCommentWithEmail(t *testing.T, db *sql.DB, id int64, email string) {
	t.Helper()
	now := time.Now().Unix()
	if _, err := db.Exec(
		`INSERT INTO comments (id, page_id, site, nick, email, content, parent_id, root_id, is_audited, create_time, update_time)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0, 1, ?, ?)`,
		id, "/test", "default", "tester", email, "test content", now, now,
	); err != nil {
		t.Fatalf("插入带邮箱测试评论 id=%d 失败: %v", id, err)
	}
}

// writeStaleCache 写一个超过 TTL 的过期缓存文件（stale-if-error 兜底场景用）
func writeStaleCache(t *testing.T, hash string) {
	t.Helper()
	path := avatarFilePath(hash, ".jpg")
	if err := os.WriteFile(path, []byte("stale-img"), 0o644); err != nil {
		t.Fatalf("写缓存文件失败: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("修改文件时间失败: %v", err)
	}
}

// TestAvatar_MissingComment_NoFailMarker 评论不存在：返回 404 且不落任何文件（防 ID 遍历放大磁盘）
func TestAvatar_MissingComment_NoFailMarker(t *testing.T) {
	svc := testService(t)
	withTempAvatarDir(t)

	if _, _, err := svc.Avatar(99999); err != model.ErrNotFound {
		t.Fatalf("评论不存在应返回 ErrNotFound, got %v", err)
	}
	if entries, err := os.ReadDir(avatarDir); err == nil && len(entries) != 0 {
		t.Fatalf("不存在的评论不应产生缓存文件, got %d 个文件", len(entries))
	}
}

// TestAvatar_NoEmail_NoFailMarker 无邮箱评论：返回 404 且不落盘
func TestAvatar_NoEmail_NoFailMarker(t *testing.T) {
	svc := testService(t)
	insertTestComment(t, svc.DB, 1, 0) // 测试评论默认 email 为空
	withTempAvatarDir(t)

	if _, _, err := svc.Avatar(1); err != model.ErrNotFound {
		t.Fatalf("无邮箱评论应返回 ErrNotFound, got %v", err)
	}
	if entries, err := os.ReadDir(avatarDir); err == nil && len(entries) != 0 {
		t.Fatalf("无邮箱评论不应产生缓存文件, got %d 个文件", len(entries))
	}
}

// TestAvatarReadCache_TTL 成功缓存惰性 TTL：新鲜文件命中，修改时间超过 TTL 视为 miss
func TestAvatarReadCache_TTL(t *testing.T) {
	withTempAvatarDir(t)

	hash := "abc123"
	path := avatarFilePath(hash, ".jpg")
	if err := os.WriteFile(path, []byte("img"), 0o644); err != nil {
		t.Fatalf("写缓存文件失败: %v", err)
	}
	if _, _, ok := readAvatarCache(hash, 7*24*time.Hour); !ok {
		t.Fatal("新写入的缓存应命中")
	}

	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("修改文件时间失败: %v", err)
	}
	if _, _, ok := readAvatarCache(hash, 7*24*time.Hour); ok {
		t.Fatal("超过 TTL 的缓存应视为 miss")
	}
}

// TestAvatarReadFile_StaleFallback 降级兜底：过期缓存 readAvatarCache 视为 miss，
// 但 readAvatarFile 仍可读出，供上游故障时 stale-if-error 降级返回
func TestAvatarReadFile_StaleFallback(t *testing.T) {
	withTempAvatarDir(t)

	hash := "abc123"
	path := avatarFilePath(hash, ".jpg")
	if err := os.WriteFile(path, []byte("img"), 0o644); err != nil {
		t.Fatalf("写缓存文件失败: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("修改文件时间失败: %v", err)
	}

	if _, _, ok := readAvatarCache(hash, 7*24*time.Hour); ok {
		t.Fatal("超过 TTL 的缓存应视为 miss")
	}
	data, _, ok := readAvatarFile(hash)
	if !ok || string(data) != "img" {
		t.Fatalf("readAvatarFile 应读出过期缓存, ok=%v data=%s", ok, data)
	}
}

// TestWriteAvatarCache_CleansOldExt 成功回源后清理旧扩展名文件：同 hash 仅保留当前扩展名，
// 避免上游 Content-Type 变化（如 jpg → png）遗留的旧扩展名文件越积越多
func TestWriteAvatarCache_CleansOldExt(t *testing.T) {
	withTempAvatarDir(t)

	hash := "abc123"
	if err := os.WriteFile(avatarFilePath(hash, ".jpg"), []byte("old"), 0o644); err != nil {
		t.Fatalf("写旧扩展名缓存失败: %v", err)
	}

	writeAvatarCache(hash, []byte("img"), "image/png")

	if data, err := os.ReadFile(avatarFilePath(hash, ".png")); err != nil || string(data) != "img" {
		t.Fatalf("新缓存应写入 .png, err=%v data=%s", err, data)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".jpg")); !os.IsNotExist(err) {
		t.Fatalf("旧扩展名 .jpg 应被清理, err=%v", err)
	}
}

// TestClearAvatarFailures 回源成功后清理 .none/.fail 标记，避免残留干扰后续语义
func TestClearAvatarFailures(t *testing.T) {
	withTempAvatarDir(t)

	hash := "abc123"
	for _, ext := range []string{".none", ".fail"} {
		if err := os.WriteFile(avatarFilePath(hash, ext), nil, 0o644); err != nil {
			t.Fatalf("写 %s 标记失败: %v", ext, err)
		}
	}

	clearAvatarFailures(hash)

	for _, ext := range []string{".none", ".fail"} {
		if _, err := os.Stat(avatarFilePath(hash, ext)); !os.IsNotExist(err) {
			t.Fatalf("%s 标记应被清理, err=%v", ext, err)
		}
	}
}

// ===== 404 vs 网络错误区分（fetchRemote） =====

// TestFetchRemote_Status404 上游 404（无头像）应返回 errAvatarNotFound，而非普通 error
func TestFetchRemote_Status404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, _, err := fetchRemote(srv.URL); !errors.Is(err, errAvatarNotFound) {
		t.Fatalf("404 应返回 errAvatarNotFound, got %v", err)
	}
}

// TestFetchRemote_Status5xx 上游 5xx 是暂时性故障，不应被误判为「无头像」
func TestFetchRemote_Status5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, _, err := fetchRemote(srv.URL); errors.Is(err, errAvatarNotFound) {
		t.Fatal("5xx 不应被误判为无头像")
	}
}

// TestFetchRemote_OK 上游 200 返回图片字节与 Content-Type
func TestFetchRemote_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake-png"))
	}))
	defer srv.Close()
	data, ct, err := fetchRemote(srv.URL)
	if err != nil {
		t.Fatalf("200 应成功, got %v", err)
	}
	if string(data) != "fake-png" || ct != "image/png" {
		t.Fatalf("data=%q ct=%s", data, ct)
	}
}

// ===== Avatar 完整链路（注入 avatarFetch 模拟上游） =====

// TestAvatar_Upstream404_NoStaleFallback 上游 404：即使存在过期缓存也不 stale 兜底
// （头像被删除应立即回退字母头像），且只写 .none 标记、不写 .fail
func TestAvatar_Upstream404_NoStaleFallback(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "old@example.com")
	withTempAvatarDir(t)

	hash := emailHash("old@example.com")
	writeStaleCache(t, hash)

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return nil, "", errAvatarNotFound
	}
	if _, _, err := svc.Avatar(1); err != model.ErrNotFound {
		t.Fatalf("上游 404 应返回 ErrNotFound（不 stale 兜底）, got %v", err)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".none")); err != nil {
		t.Fatal("应写入 .none 标记")
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err == nil {
		t.Fatal("不应写入 .fail 标记")
	}
}

// TestAvatar_NotFoundMarker_Direct404 .none 标记存在时：即使有过期缓存也直接 404（无头像语义）
func TestAvatar_NotFoundMarker_Direct404(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "old@example.com")
	withTempAvatarDir(t)

	hash := emailHash("old@example.com")
	writeStaleCache(t, hash)
	if err := os.WriteFile(avatarFilePath(hash, ".none"), nil, 0o644); err != nil {
		t.Fatalf("写 .none 标记失败: %v", err)
	}

	if _, _, err := svc.Avatar(1); err != model.ErrNotFound {
		t.Fatalf(".none 存在时应直接 404, got %v", err)
	}
}

// TestAvatar_UpstreamError_StaleFallback 网络故障：有过期缓存 → stale 兜底返回旧图 + 写 .fail 标记
func TestAvatar_UpstreamError_StaleFallback(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "old@example.com")
	withTempAvatarDir(t)

	hash := emailHash("old@example.com")
	writeStaleCache(t, hash)

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return nil, "", errors.New("connection refused")
	}
	data, _, err := svc.Avatar(1)
	if err != nil {
		t.Fatalf("网络故障+有过期缓存应 stale 兜底, got %v", err)
	}
	if string(data) != "stale-img" {
		t.Fatalf("应返回过期缓存内容, got %q", data)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err != nil {
		t.Fatal("应写入 .fail 标记")
	}
}

// TestAvatar_UpstreamError_NoCache 网络故障 + 无任何历史缓存 → 404 + 写 .fail 标记
func TestAvatar_UpstreamError_NoCache(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "old@example.com")
	withTempAvatarDir(t)

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return nil, "", errors.New("timeout")
	}
	if _, _, err := svc.Avatar(1); err != model.ErrNotFound {
		t.Fatalf("网络故障+无缓存应返回 ErrNotFound, got %v", err)
	}
	hash := emailHash("old@example.com")
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err != nil {
		t.Fatal("应写入 .fail 标记")
	}
	if _, err := os.Stat(avatarFilePath(hash, ".none")); err == nil {
		t.Fatal("网络故障不应写入 .none 标记")
	}
}

// TestAvatar_Success_ClearsFailures 回源成功：写缓存并清理 .none/.fail 标记
func TestAvatar_Success_ClearsFailures(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "old@example.com")
	withTempAvatarDir(t)

	hash := emailHash("old@example.com")
	if err := os.WriteFile(avatarFilePath(hash, ".none"), nil, 0o644); err != nil {
		t.Fatalf("写 .none 标记失败: %v", err)
	}
	if err := os.WriteFile(avatarFilePath(hash, ".fail"), nil, 0o644); err != nil {
		t.Fatalf("写 .fail 标记失败: %v", err)
	}
	// 将标记改为 25h 前（已过期）：确保 Avatar 不被拦截、走回源成功路径，
	// 以验证「回源成功后清理残留标记」的行为
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(avatarFilePath(hash, ".none"), old, old); err != nil {
		t.Fatalf("修改 .none 时间失败: %v", err)
	}
	if err := os.Chtimes(avatarFilePath(hash, ".fail"), old, old); err != nil {
		t.Fatalf("修改 .fail 时间失败: %v", err)
	}

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return []byte("new-img"), "image/jpeg", nil
	}
	data, ct, err := svc.Avatar(1)
	if err != nil {
		t.Fatalf("回源成功应返回图片, got %v", err)
	}
	if string(data) != "new-img" || ct != "image/jpeg" {
		t.Fatalf("data=%q ct=%s", data, ct)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".none")); err == nil {
		t.Fatal("成功后应清理 .none 标记")
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err == nil {
		t.Fatal("成功后应清理 .fail 标记")
	}
	if _, _, ok := readAvatarCache(hash, 7*24*time.Hour); !ok {
		t.Fatal("成功后应写入新缓存")
	}
}

// TestCleanAvatarCache_RemovesInvalid 清理应删除：过期图片缓存、过期标记、残留 tmp、非法命名孤儿
func TestCleanAvatarCache_RemovesInvalid(t *testing.T) {
	svc := testService(t)
	withTempAvatarDir(t)

	hash := emailHash("expired@example.com")
	fresh := emailHash("fresh@example.com")
	// 过期图片缓存（TTL 7 天 + 1h 冗余 → 8 天前）
	stale := avatarFilePath(hash, ".jpg")
	writeWithAge(t, stale, 8*24*time.Hour)
	// 新鲜图片缓存（保留）
	freshPath := avatarFilePath(fresh, ".png")
	if err := os.WriteFile(freshPath, []byte("y"), 0o644); err != nil {
		t.Fatalf("写新鲜缓存失败: %v", err)
	}
	// 过期标记（25h 前）
	mark := avatarFilePath(hash, ".none")
	writeWithAge(t, mark, 25*time.Hour)
	// 残留 tmp（2h 前）
	tmp := filepath.Join(avatarDir, hash+"-12345.tmp")
	writeWithAge(t, tmp, 2*time.Hour)
	// 非法命名历史孤儿（旧格式 {评论ID}.ext）
	orphan := filepath.Join(avatarDir, "42.jpg")
	if err := os.WriteFile(orphan, []byte("z"), 0o644); err != nil {
		t.Fatalf("写孤儿文件失败: %v", err)
	}

	svc.cleanAvatarCache()

	for _, p := range []string{stale, mark, tmp, orphan} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("失效文件 %s 应被删除, stat err=%v", p, err)
		}
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("新鲜缓存 %s 不应被删除: %v", freshPath, err)
	}
}

// TestCleanAvatarCache_KeepsFresh 清理应保留：新鲜图片缓存、新鲜标记、进行中的 tmp
func TestCleanAvatarCache_KeepsFresh(t *testing.T) {
	svc := testService(t)
	withTempAvatarDir(t)

	hash := emailHash("keeper@example.com")
	img := avatarFilePath(hash, ".jpg")
	if err := os.WriteFile(img, []byte("a"), 0o644); err != nil {
		t.Fatalf("写缓存失败: %v", err)
	}
	mark := avatarFilePath(hash, ".fail")
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatalf("写标记失败: %v", err)
	}
	// 10 分钟前的 tmp（视为进行中的原子写）
	tmp := filepath.Join(avatarDir, hash+"-67890.tmp")
	writeWithAge(t, tmp, 10*time.Minute)

	svc.cleanAvatarCache()

	for _, p := range []string{img, mark, tmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("新鲜文件 %s 不应被删除: %v", p, err)
		}
	}
}

// writeWithAge 写文件并把修改时间改为 age 之前
func writeWithAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("写文件 %s 失败: %v", path, err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("修改文件时间 %s 失败: %v", path, err)
	}
}

// TestDeleteComment_ClearsAvatarEmailCache 删除评论同步清理 avatar:email 内存映射：
// 评论删除后头像代理立即 404，关闭「已删评论头像最多可访问 60s」窗口。
func TestDeleteComment_ClearsAvatarEmailCache(t *testing.T) {
	svc := testService(t)
	withTempAvatarDir(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "del@example.com")

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return []byte("img"), "image/jpeg", nil
	}
	if _, _, err := svc.Avatar(1); err != nil {
		t.Fatalf("回源应成功, got %v", err)
	}
	if _, ok := svc.avatarEmailCache(1); !ok {
		t.Fatal("Avatar 后应写入 avatar:email:1 内存映射")
	}

	if _, err := svc.DeleteComment(1); err != nil {
		t.Fatalf("删除评论失败: %v", err)
	}
	if _, ok := svc.avatarEmailCache(1); ok {
		t.Fatal("删除评论后应清理 avatar:email:1 内存映射")
	}
	if _, _, err := svc.Avatar(1); err != model.ErrNotFound {
		t.Fatalf("删除后头像应返回 ErrNotFound, got %v", err)
	}
}

// TestDeleteComment_Root_ClearsSubtreeAvatarMaps 删除根评论时，级联子树的
// avatar:email 内存映射一并清理（根 + 全部回复）。
func TestDeleteComment_Root_ClearsSubtreeAvatarMaps(t *testing.T) {
	svc := testService(t)
	withTempAvatarDir(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "root@example.com")
	insertTestCommentWithEmail(t, svc.DB, 2, "sub@example.com")
	// 把 id=2 挂到根 id=1 下（parent_id=1, root_id=1）
	if _, err := svc.DB.Exec(`UPDATE comments SET parent_id = 1, root_id = 1 WHERE id = 2`); err != nil {
		t.Fatalf("挂子树失败: %v", err)
	}

	svc.avatarFetch = func(url string) ([]byte, string, error) {
		return []byte("img"), "image/jpeg", nil
	}
	for _, id := range []int64{1, 2} {
		if _, _, err := svc.Avatar(id); err != nil {
			t.Fatalf("回源评论 %d 失败: %v", id, err)
		}
		if _, ok := svc.avatarEmailCache(id); !ok {
			t.Fatalf("Avatar 后应写入 avatar:email:%d 内存映射", id)
		}
	}

	if _, err := svc.DeleteComment(1); err != nil {
		t.Fatalf("删除根评论失败: %v", err)
	}
	for _, id := range []int64{1, 2} {
		if _, ok := svc.avatarEmailCache(id); ok {
			t.Fatalf("删除根评论后应清理 avatar:email:%d（级联子树）", id)
		}
	}
}
