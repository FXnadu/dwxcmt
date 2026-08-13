package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 连贯多步骤流程模拟（贴近真实调用时序） =====

// TestAvatarFlow_QQ_CompleteLifecycle 完整生命周期流程：
// 首次回源成功 → 缓存命中（不回源）→ 缓存过期后上游故障（stale 兜底 + .fail 标记）
// → .fail 拦截期内不再回源 → 上游恢复（回源成功 + 清标记 + 新缓存）。
// 同时断言 QQ 邮箱分流到 qlogo 且 URL 携带 QQ 号。
func TestAvatarFlow_QQ_CompleteLifecycle(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "1234567@qq.com")
	withTempAvatarDir(t)

	var (
		mu       sync.Mutex
		lastURL  string
		mode     string // "ok" | "fail"
		curBytes []byte
		requests atomic.Int64
	)
	svc.avatarFetch = func(url string) ([]byte, string, error) {
		requests.Add(1)
		mu.Lock()
		defer mu.Unlock()
		lastURL = url
		if mode == "fail" {
			return nil, "", errors.New("connection refused")
		}
		return curBytes, "image/jpeg", nil
	}
	calls := func() int64 { return requests.Load() }

	hash := emailHash("1234567@qq.com")

	// 1) 首次请求：回源 qlogo 成功，返回图片并落盘
	mu.Lock()
	mode, curBytes = "ok", []byte("qq-avatar-v1")
	mu.Unlock()
	data, ct, err := svc.Avatar(1)
	if err != nil {
		t.Fatalf("首次回源应成功, got %v", err)
	}
	if string(data) != "qq-avatar-v1" || ct != "image/jpeg" {
		t.Fatalf("data=%q ct=%s", data, ct)
	}
	mu.Lock()
	url := lastURL
	mu.Unlock()
	if !strings.Contains(url, "q1.qlogo.cn") || !strings.Contains(url, "nk=1234567") {
		t.Fatalf("QQ 邮箱应分流到 qlogo 且携带 QQ 号, got %s", url)
	}
	if _, _, ok := readAvatarCache(hash, svc.avatarCacheTTL()); !ok {
		t.Fatal("首次回源后应落盘新鲜缓存")
	}

	// 2) 缓存命中：第二次请求不再回源，内容一致
	n := calls()
	data2, _, err2 := svc.Avatar(1)
	if err2 != nil || string(data2) != "qq-avatar-v1" {
		t.Fatalf("缓存命中应返回原图, err=%v data=%q", err2, data2)
	}
	if calls() != n {
		t.Fatalf("缓存命中不应触发回源, calls=%d → %d", n, calls())
	}

	// 3) 模拟 8 天后缓存过期（仅改 mtime，保留原图内容供 stale 兜底断言）
	path := avatarFilePath(hash, ".jpg")
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("修改缓存 mtime 失败: %v", err)
	}

	// 4) 上游故障：stale-if-error 兜底返回过期缓存，并写 .fail 标记
	mu.Lock()
	mode = "fail"
	mu.Unlock()
	data, _, err = svc.Avatar(1)
	if err != nil || string(data) != "qq-avatar-v1" {
		t.Fatalf("上游故障应 stale 兜底返回旧图, err=%v data=%q", err, data)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err != nil {
		t.Fatal("上游故障应写入 .fail 标记")
	}

	// 5) .fail 拦截期内：继续返回 stale 图，不再回源
	n = calls()
	data, _, err = svc.Avatar(1)
	if err != nil || string(data) != "qq-avatar-v1" {
		t.Fatalf(".fail 拦截期内应持续返回 stale 图, err=%v data=%q", err, data)
	}
	if calls() != n {
		t.Fatalf(".fail 拦截期内不应回源, calls=%d → %d", n, calls())
	}

	// 6) 上游恢复：清除 .fail 标记（模拟 24h 标记到期失效后重试），回源成功、清理标记、写入新缓存
	mu.Lock()
	mode, curBytes = "ok", []byte("qq-avatar-v2")
	mu.Unlock()
	if err := os.Remove(avatarFilePath(hash, ".fail")); err != nil {
		t.Fatalf("清除 .fail 标记失败: %v", err)
	}
	data, _, err = svc.Avatar(1)
	if err != nil || string(data) != "qq-avatar-v2" {
		t.Fatalf("上游恢复应回源成功, err=%v data=%q", err, data)
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err == nil {
		t.Fatal("回源成功后应清理 .fail 标记")
	}
	fresh, _, ok := readAvatarCache(hash, svc.avatarCacheTTL())
	if !ok || string(fresh) != "qq-avatar-v2" {
		t.Fatalf("回源成功后应写入新缓存, ok=%v data=%q", ok, fresh)
	}
}

// TestAvatarFlow_NonQQ_CravatarURL 非 QQ 邮箱分流到 Cravatar：
// URL 应携带邮箱 md5 与 d=404（无头像 → 404 → 前端字母头像）、s=48。
func TestAvatarFlow_NonQQ_CravatarURL(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "user@example.com")
	withTempAvatarDir(t)

	var gotURL string
	svc.avatarFetch = func(url string) ([]byte, string, error) {
		gotURL = url
		return []byte("cv-avatar"), "image/png", nil
	}
	if _, _, err := svc.Avatar(1); err != nil {
		t.Fatalf("回源应成功, got %v", err)
	}
	hash := emailHash("user@example.com")
	want := "https://cravatar.cn/avatar/" + hash + "?d=404&s=48"
	if gotURL != want {
		t.Fatalf("非 QQ 邮箱应分流到 Cravatar, got %s want %s", gotURL, want)
	}
}

// TestAvatarFlow_FailMarkerExpired_Retry 过期 .fail 标记不拦截回源
// （「无 → 新注册头像」场景：24h 后允许重试上游）。
func TestAvatarFlow_FailMarkerExpired_Retry(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "retry@example.com")
	withTempAvatarDir(t)

	hash := emailHash("retry@example.com")
	writeWithAge(t, avatarFilePath(hash, ".fail"), 25*time.Hour) // 25h 前写入，已过期

	var calls atomic.Int64
	svc.avatarFetch = func(url string) ([]byte, string, error) {
		calls.Add(1)
		return []byte("img"), "image/jpeg", nil
	}
	data, _, err := svc.Avatar(1)
	if err != nil || string(data) != "img" {
		t.Fatalf("过期 .fail 不应拦截回源, err=%v data=%q", err, data)
	}
	if calls.Load() != 1 {
		t.Fatalf("应回源 1 次, got %d", calls.Load())
	}
	if _, err := os.Stat(avatarFilePath(hash, ".fail")); err == nil {
		t.Fatal("回源成功后应清理过期 .fail 标记")
	}
}

// TestAvatarFlow_ConcurrentSameEmail 完整链路并发回源同一邮箱：
// 每轮 8 个 goroutine 同时请求同一评论的头像（同一邮箱 → 同一 hash），
// 混合 Content-Type（jpg/png）触发 cleanupAvatarCache 清理旧扩展名，
// 多轮循环 + 每轮清空缓存目录强制所有 goroutine 走回源写路径。
// 验证分片锁修复后：不 panic、不残留 tmp、每轮最终恰有一个扩展名缓存。
// 对照实验：临时移除 writeAvatarCache 中的分片锁后本测试必失败（缓存互删为 0）。
func TestAvatarFlow_ConcurrentSameEmail(t *testing.T) {
	svc := testService(t)
	insertTestCommentWithEmail(t, svc.DB, 1, "hot@example.com")
	withTempAvatarDir(t)

	var requests atomic.Int64
	svc.avatarFetch = func(url string) ([]byte, string, error) {
		requests.Add(1)
		// 混合 Content-Type，模拟上游变化触发 cleanupAvatarCache 清理旧扩展名
		if requests.Load()%2 == 0 {
			return []byte("png-avatar"), "image/png", nil
		}
		return []byte("jpg-avatar"), "image/jpeg", nil
	}

	const (
		rounds  = 20 // 多轮：竞态是概率性事件，单轮通过不代表稳定
		workers = 8
	)
	for round := 0; round < rounds; round++ {
		// 清空上一轮缓存：否则后续轮次全部缓存命中，测不到并发回源写
		if entries, err := os.ReadDir(avatarDir); err == nil {
			for _, e := range entries {
				os.Remove(filepath.Join(avatarDir, e.Name()))
			}
		}

		var wg sync.WaitGroup
		errs := make([]error, workers)
		datas := make([][]byte, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				datas[i], _, errs[i] = svc.Avatar(1)
			}(i)
		}
		wg.Wait()

		for i := 0; i < workers; i++ {
			if errs[i] != nil {
				t.Fatalf("第 %d 轮 goroutine %d 应成功返回图片, got %v", round, i, errs[i])
			}
			if len(datas[i]) == 0 {
				t.Fatalf("第 %d 轮 goroutine %d 返回空数据", round, i)
			}
		}

		entries, err := os.ReadDir(avatarDir)
		if err != nil {
			t.Fatalf("第 %d 轮读取缓存目录失败: %v", round, err)
		}
		var imgs, tmps int
		for _, e := range entries {
			switch {
			case avatarTmpRE.MatchString(e.Name()):
				tmps++
			case avatarFileRE.MatchString(e.Name()):
				imgs++
			}
		}
		if tmps != 0 {
			t.Fatalf("第 %d 轮：并发回源不应残留 tmp 文件, got %d", round, tmps)
		}
		if imgs != 1 {
			t.Fatalf("第 %d 轮：同邮箱并发回源最终应恰有 1 个扩展名缓存, got %d（分片锁缺失时 cleanup 互删导致）", round, imgs)
		}
	}
}

// TestWriteAvatarCache_ConcurrentMixedExt 竞态最小复现（直接打 writeAvatarCache）：
// 8 个 goroutine 并发写入同一 hash、混合扩展名（jpg/png），多轮循环提高概率性竞态暴露率。
// 修复前（无 avatarHashLocks 分片锁）：两个写者各自 cleanupAvatarCache 会删掉对方刚落盘的文件
// → 最终缓存文件数可能为 0；修复后每轮必须恒为 1 个缓存文件且无 tmp 残留。
// 对照实验：临时移除 writeAvatarCache 中的分片锁后，本测试必失败，证明测试能抓住该竞态。
func TestWriteAvatarCache_ConcurrentMixedExt(t *testing.T) {
	withTempAvatarDir(t)
	hash := emailHash("race@example.com")

	const (
		rounds = 20 // 多轮：竞态是概率性事件，单轮通过不代表稳定
		workers = 8
	)
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i%2 == 0 {
					writeAvatarCache(hash, []byte("jpg-content"), "image/jpeg")
				} else {
					writeAvatarCache(hash, []byte("png-content"), "image/png")
				}
			}(i)
		}
		wg.Wait()

		entries, err := os.ReadDir(avatarDir)
		if err != nil {
			t.Fatalf("第 %d 轮读取缓存目录失败: %v", round, err)
		}
		var imgs, tmps int
		for _, e := range entries {
			switch {
			case avatarTmpRE.MatchString(e.Name()):
				tmps++
			case avatarFileRE.MatchString(e.Name()):
				imgs++
			}
		}
		if tmps != 0 {
			t.Fatalf("第 %d 轮：并发写入不应残留 tmp 文件, got %d", round, tmps)
		}
		if imgs != 1 {
			t.Fatalf("第 %d 轮：并发写入同一 hash 后应恰有 1 个扩展名缓存, got %d（分片锁缺失时 cleanup 互删导致）", round, imgs)
		}
	}
}
