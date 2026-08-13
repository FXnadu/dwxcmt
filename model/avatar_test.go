package model

import (
	"strings"
	"testing"
)

// 隐私核心：候选只含本服务代理地址，不含任何第三方直链（cravatar/gravatar），
// 公开响应中不出现邮箱 md5，杜绝字典爆破与跨站关联风险
func TestAvatarCandidatesNoThirdPartyURL(t *testing.T) {
	for _, email := range []string{"test@example.com", "12345@qq.com"} {
		for _, u := range avatarCandidates(1, email) {
			if strings.Contains(u, "cravatar.cn") || strings.Contains(u, "gravatar.com") {
				t.Fatalf("候选不应含第三方直链: %s", u)
			}
		}
	}
}

// 有邮箱（QQ 与非 QQ 统一）：唯一候选为本服务代理地址
func TestAvatarCandidatesProxyOnly(t *testing.T) {
	for _, email := range []string{"Test@Example.com", "12345@qq.com"} {
		cands := avatarCandidates(42, email)
		if len(cands) != 1 {
			t.Fatalf("期望 1 个候选（仅本服务代理），实际 %d 个: %v", len(cands), cands)
		}
		if cands[0] != "/api/v1/avatars/42" {
			t.Fatalf("候选应为本服务代理地址，实际: %s", cands[0])
		}
	}
}

// 无邮箱：无候选（前端回退字母头像）
func TestAvatarCandidatesNoEmail(t *testing.T) {
	if cands := avatarCandidates(1, ""); len(cands) != 0 {
		t.Fatalf("无邮箱应无候选，实际: %v", cands)
	}
}

// 最坏情况等待时长：唯一候选（本服务代理）× 1.5s 超时 = 1.5s，不再有第三方串行拖累
func TestAvatarWorstCaseLatency(t *testing.T) {
	cands := avatarCandidates(1, "test@example.com")
	const timeoutMs = 1500 // front/comment.js AVATAR_TIMEOUT
	worst := len(cands) * timeoutMs
	if worst > 3000 {
		t.Fatalf("最坏等待 %dms 过长", worst)
	}
}
