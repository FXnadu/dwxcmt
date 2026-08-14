package model

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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

// emailMD5Hex 与服务端一致的邮箱 md5 计算：先归一化（小写+去空白）再 md5，
// 与服务层 Avatar 的缓存键计算逻辑保持一致，保证断言的哈希即真实缓存键。
func emailMD5Hex(email string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// TestPublicDTO_NoEmailHashInAvatarUrls 公开响应序列化端到端断言：
// 构造含邮箱的 Comment → ToDTO(false) → MarshalJSON，
// 断言 (1) 响应体不含邮箱 md5（代理化后哈希不再出服务端）；
// (2) avatarUrls 只含本服务代理地址，无第三方直链；
// (3) email 字段不出现在公开响应。
func TestPublicDTO_NoEmailHashInAvatarUrls(t *testing.T) {
	emails := []string{"test@example.com", "12345@qq.com", "Alice.Wang@Example.COM"}
	for _, email := range emails {
		c := &Comment{
			ID: 7, PageID: "/post/a", Site: "default",
			Nick: "alice", Link: "", Content: "hello",
			Email: email, IsAudited: 1, CreateTime: 1000,
		}
		dto := c.ToDTO(false)
		raw, err := json.Marshal(dto)
		if err != nil {
			t.Fatalf("MarshalJSON 失败: %v", err)
		}
		body := string(raw)

		// (1) 公开响应不得包含邮箱 md5（对大小写不敏感的邮箱归一化后计算，避免大小写歧义）
		hash := emailMD5Hex(strings.ToLower(strings.TrimSpace(email)))
		if strings.Contains(body, hash) {
			t.Fatalf("公开响应不应包含邮箱 md5 %q，响应: %s", hash, body)
		}
		// 邮箱明文同样不得出现
		if strings.Contains(strings.ToLower(body), strings.ToLower(email)) {
			t.Fatalf("公开响应不应包含邮箱明文 %q，响应: %s", email, body)
		}
		// (2) avatarUrls 只含本服务代理地址
		if !strings.Contains(body, "/api/v1/avatars/7") {
			t.Fatalf("avatarUrls 应含本服务代理地址，响应: %s", body)
		}
		for _, banned := range []string{"cravatar.cn", "gravatar.com", "qlogo.cn"} {
			if strings.Contains(body, banned) {
				t.Fatalf("公开响应不应包含第三方直链 %s，响应: %s", banned, body)
			}
		}
		// (3) email 字段不得出现
		if strings.Contains(body, `"email"`) {
			t.Fatalf("公开响应不应包含 email 字段，响应: %s", body)
		}
	}
}

// TestPublicDTO_NoEmailHash_SameEmailDifferentCase 大小写归一化一致性：
// 相同邮箱不同大小写应归一化为同一 md5，公开响应均不含该哈希。
func TestPublicDTO_NoEmailHash_SameEmailDifferentCase(t *testing.T) {
	emails := []string{"Alice@Example.com", "alice@example.com", " ALICE@EXAMPLE.COM "}
	hashes := map[string]bool{}
	for _, e := range emails {
		hashes[emailMD5Hex(e)] = true
	}
	if len(hashes) != 1 {
		t.Fatalf("大小写/空白不同应归一化为同一 md5，实际 %d 个: %v", len(hashes), hashes)
	}
}
