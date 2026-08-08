package utils

import (
	"net/http/httptest"
	"testing"
)

// resetProxies 在每个测试后清空可信代理列表，保证测试隔离
func resetProxies(t *testing.T) {
	t.Cleanup(func() {
		_ = SetTrustedProxies(nil)
	})
}

// ===== P0-2: ClientIP 可信代理白名单测试 =====

func TestClientIP_NoTrustedProxies_IgnoresXFF(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies(nil)

	// 攻击者直连服务，伪造 X-Forwarded-For
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("无可信代理时应忽略 XFF 返回 RemoteAddr, got %s", ip)
	}
}

func TestClientIP_TrustedProxy_ReadsXFF(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32"})

	// Nginx 反代场景：RemoteAddr=127.0.0.1 可信，读取 XFF
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")

	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("可信代理应读取 XFF, got %s", ip)
	}
}

func TestClientIP_UntrustedRemoteAddr_IgnoresXFF(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32"})

	// 攻击者直连（RemoteAddr 不在可信列表），伪造 XFF
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("不可信 RemoteAddr 应忽略 XFF 返回真实 RemoteAddr, got %s", ip)
	}
}

func TestClientIP_MultiLevelProxyChain(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})

	// 链路: 用户(203.0.113.5) → CDN(10.0.0.1) → Nginx(127.0.0.1)
	// XFF: "203.0.113.5, 10.0.0.1"
	// 应从右向左跳过 10.0.0.1（可信），取 203.0.113.5
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")

	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("多级代理应跳过可信代理取真实 IP, got %s", ip)
	}
}

func TestClientIP_AllProxiesTrusted_ReturnsLeftmost(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32", "10.0.0.0/8"})

	// 所有 XFF 条目都是可信代理 → 退化到取第一个
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")

	ip := ClientIP(r)
	if ip != "10.0.0.1" {
		t.Errorf("全可信链应返回最左侧, got %s", ip)
	}
}

func TestClientIP_XRealIP_Fallback(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32"})

	// 无 XFF，有 X-Real-IP
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Real-IP", "203.0.113.5")

	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Errorf("可信代理时应读取 X-Real-IP, got %s", ip)
	}
}

func TestClientIP_NoHeaders_ReturnsRemoteAddr(t *testing.T) {
	resetProxies(t)
	_ = SetTrustedProxies([]string{"127.0.0.1/32"})

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"

	ip := ClientIP(r)
	if ip != "127.0.0.1" {
		t.Errorf("无转发头时应返回 RemoteAddr, got %s", ip)
	}
}

func TestSetTrustedProxies_Invalid(t *testing.T) {
	err := SetTrustedProxies([]string{"not-an-ip"})
	if err == nil {
		t.Error("非法 IP 应返回错误")
	}
}

func TestSetTrustedProxies_EmptyList(t *testing.T) {
	err := SetTrustedProxies(nil)
	if err != nil {
		t.Errorf("空列表不应报错, got %v", err)
	}
	// 空列表 → 不信任任何代理
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	ip := ClientIP(r)
	if ip != "127.0.0.1" {
		t.Errorf("空可信列表应忽略 XFF, got %s", ip)
	}
}
