package service

import (
	"sync"
	"testing"
)

// ===== P1-7: CheckAndBumpDailyCount 原子日限流测试 =====

func TestCheckAndBump_WithinLimit(t *testing.T) {
	svc := testService(t)
	cfg := svc.Cfg.RateLimit
	limit := cfg.CommentsPerDay // 默认 20

	for i := 0; i < limit; i++ {
		ok, err := svc.CheckAndBumpDailyCount("1.2.3.4")
		if err != nil {
			t.Fatalf("第 %d 次调用失败: %v", i+1, err)
		}
		if !ok {
			t.Fatalf("第 %d 次调用应在限额内返回 true", i+1)
		}
	}
	// 第 limit+1 次应被拒绝
	ok, err := svc.CheckAndBumpDailyCount("1.2.3.4")
	if err != nil {
		t.Fatalf("超限调用报错: %v", err)
	}
	if ok {
		t.Error("超过限额后应返回 false")
	}
}

func TestCheckAndBump_IPIsolation(t *testing.T) {
	svc := testService(t)
	limit := svc.Cfg.RateLimit.CommentsPerDay

	// IP A 耗尽限额
	for i := 0; i < limit; i++ {
		ok, _ := svc.CheckAndBumpDailyCount("10.0.0.1")
		if !ok {
			t.Fatalf("IP A 第 %d 次应在限额内", i+1)
		}
	}
	// IP B 不受影响
	ok, err := svc.CheckAndBumpDailyCount("10.0.0.2")
	if err != nil || !ok {
		t.Errorf("不同 IP 不应互相影响, ok=%v err=%v", ok, err)
	}
}

func TestCheckAndBump_DisabledWhenLimitZero(t *testing.T) {
	svc := testService(t)
	svc.Cfg.RateLimit.CommentsPerDay = 0

	for i := 0; i < 5; i++ {
		ok, err := svc.CheckAndBumpDailyCount("1.1.1.1")
		if err != nil || !ok {
			t.Fatalf("限额为 0（禁用）时恒应放行, ok=%v err=%v", ok, err)
		}
	}
}

// TestCheckAndBump_Concurrent 并发竞态测试：
// 10 个 goroutine 同时竞争 3 次限额，最终成功数必须严格等于 3（≤ 上限）。
// 旧实现（读-写分离）在此测试下会突破上限。
func TestCheckAndBump_Concurrent(t *testing.T) {
	svc := testService(t)
	svc.Cfg.RateLimit.CommentsPerDay = 3

	const workers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := svc.CheckAndBumpDailyCount("203.0.113.5")
			if err != nil {
				t.Errorf("并发调用报错: %v", err)
				return
			}
			if ok {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if success != 3 {
		t.Errorf("并发 10 请求、限额 3 时成功数应为 3（原子性保证不超限），got %d", success)
	}
}
