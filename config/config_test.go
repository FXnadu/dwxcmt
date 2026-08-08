package config

import (
	"errors"
	"net"
	"testing"

	"dwxcmt/pkg/utils"
)

// ===== P0-1: JWT 默认密钥校验 =====

func TestDefault_UsesDefaultJWTSecret(t *testing.T) {
	cfg := Default()
	if cfg.Auth.JWTSecret != DefaultJWTSecret {
		t.Errorf("默认配置应使用 DefaultJWTSecret")
	}
}

func TestValidate_DefaultSecret(t *testing.T) {
	cfg := Default()
	err := cfg.Validate()
	if !errors.Is(err, ErrInsecureJWTSecret) {
		t.Errorf("默认密钥应返回 ErrInsecureJWTSecret, got %v", err)
	}
}

func TestValidate_EmptySecret(t *testing.T) {
	cfg := Default()
	cfg.Auth.JWTSecret = ""
	err := cfg.Validate()
	if !errors.Is(err, ErrInsecureJWTSecret) {
		t.Errorf("空密钥应返回 ErrInsecureJWTSecret, got %v", err)
	}
}

func TestValidate_CustomSecret_Passes(t *testing.T) {
	cfg := Default()
	cfg.Auth.JWTSecret = "my-custom-random-secret-32chars!!"
	err := cfg.Validate()
	if err != nil {
		t.Errorf("自定义安全密钥应通过校验, got %v", err)
	}
}

// ===== P0-2: 可信代理 CIDR 校验 =====

func TestValidate_InvalidTrustedProxy(t *testing.T) {
	cfg := Default()
	cfg.Auth.JWTSecret = "my-custom-random-secret-32chars!!"
	cfg.TrustedProxy.Proxies = []string{"not-a-cidr"}
	err := cfg.Validate()
	if err == nil {
		t.Error("非法 CIDR 应返回错误")
	}
}

func TestValidate_ValidTrustedProxies(t *testing.T) {
	cfg := Default()
	cfg.Auth.JWTSecret = "my-custom-random-secret-32chars!!"
	cfg.TrustedProxy.Proxies = []string{"127.0.0.1/32", "10.0.0.0/8", "192.168.1.1"}
	err := cfg.Validate()
	if err != nil {
		t.Errorf("合法代理列表应通过校验, got %v", err)
	}
}

func TestParseTrustedProxy_CIDR(t *testing.T) {
	n, err := utils.ParseTrustedProxy("10.0.0.0/8")
	if err != nil {
		t.Fatalf("解析 CIDR 失败: %v", err)
	}
	if !n.Contains(net.ParseIP("10.1.2.3")) {
		t.Error("10.0.0.0/8 应包含 10.1.2.3")
	}
	if n.Contains(net.ParseIP("192.168.1.1")) {
		t.Error("10.0.0.0/8 不应包含 192.168.1.1")
	}
}

func TestParseTrustedProxy_BareIPv4(t *testing.T) {
	n, err := utils.ParseTrustedProxy("127.0.0.1")
	if err != nil {
		t.Fatalf("解析裸 IP 失败: %v", err)
	}
	ones, bits := n.Mask.Size()
	if ones != 32 || bits != 32 {
		t.Errorf("IPv4 裸 IP 应补 /32, got /%d of /%d", ones, bits)
	}
	if !n.Contains(net.ParseIP("127.0.0.1")) {
		t.Error("应包含 127.0.0.1")
	}
}

func TestParseTrustedProxy_BareIPv6(t *testing.T) {
	n, err := utils.ParseTrustedProxy("::1")
	if err != nil {
		t.Fatalf("解析 IPv6 失败: %v", err)
	}
	ones, bits := n.Mask.Size()
	if ones != 128 || bits != 128 {
		t.Errorf("IPv6 裸 IP 应补 /128, got /%d of /%d", ones, bits)
	}
}

func TestParseTrustedProxy_Invalid(t *testing.T) {
	_, err := utils.ParseTrustedProxy("not-an-ip")
	if err == nil {
		t.Error("非法字符串应返回错误")
	}
}
