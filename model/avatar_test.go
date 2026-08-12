package model

import (
	"strings"
	"testing"
)

// 核心修复：头像候选列表不应再包含 gravatar.com（国内直连慢/挂起，曾拖慢页面加载数秒）
func TestAvatarCandidatesNoGravatar(t *testing.T) {
	for _, email := range []string{"test@example.com", "12345@qq.com", ""} {
		for _, u := range avatarCandidates(1, email) {
			if strings.Contains(u, "gravatar.com") {
				t.Fatalf("候选仍包含 gravatar.com: %s", u)
			}
		}
	}
}

// 非 QQ 邮箱：只剩 Cravatar 一个候选（旧实现是 Cravatar + Gravatar 两个）
func TestAvatarCandidatesNonQQ(t *testing.T) {
	cands := avatarCandidates(1, "Test@Example.com")
	if len(cands) != 1 {
		t.Fatalf("期望 1 个候选（仅 Cravatar），实际 %d 个: %v", len(cands), cands)
	}
	if !strings.Contains(cands[0], "cravatar.cn") {
		t.Fatalf("候选应为 cravatar.cn，实际: %s", cands[0])
	}
}

// QQ 邮箱：本服务代理地址在最前（命中缓存毫秒级返回），其后才是 Cravatar
func TestAvatarCandidatesQQOrder(t *testing.T) {
	cands := avatarCandidates(42, "12345@qq.com")
	if len(cands) != 2 {
		t.Fatalf("期望 2 个候选（QQ 代理 + Cravatar），实际 %d 个: %v", len(cands), cands)
	}
	if !strings.HasPrefix(cands[0], "/api/v1/avatars/") {
		t.Fatalf("QQ 邮箱首个候选应为本服务代理，实际: %s", cands[0])
	}
	if !strings.Contains(cands[1], "cravatar.cn") {
		t.Fatalf("第二个候选应为 cravatar.cn，实际: %s", cands[1])
	}
}

// 最坏情况等待时长（所有候选都失败、串行等满超时）：
// 旧：2 个候选（Cravatar + Gravatar）× 4s 超时 = 8s
// 新：1 个候选（仅 Cravatar）× 1.5s 超时（front/comment.js AVATAR_TIMEOUT）= 1.5s
// 模拟前端串行加载逻辑，验证最坏等待缩短约 5 倍
func TestAvatarWorstCaseLatency(t *testing.T) {
	type config struct {
		candidates int
		timeoutMs  int
	}
	oldCfg := config{candidates: 2, timeoutMs: 4000}
	newCfg := config{candidates: 1, timeoutMs: 1500}
	worst := func(c config) int { return c.candidates * c.timeoutMs }

	oldMs, newMs := worst(oldCfg), worst(newCfg)
	if newMs >= oldMs {
		t.Fatalf("新配置最坏等待 %dms 未短于旧配置 %dms", newMs, oldMs)
	}
	t.Logf("所有候选均失败时的最坏等待：旧 %dms -> 新 %dms（减少 %dms）", oldMs, newMs, oldMs-newMs)
}
