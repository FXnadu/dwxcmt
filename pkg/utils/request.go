package utils

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var (
	trustedMu    sync.RWMutex
	trustedNets  []*net.IPNet // 编译解析后的可信代理 CIDR
	trustNoProxy bool         // 兼容：列表为空时的行为，=false 表示完全不信任转发头
)

// ParseTrustedProxy 解析单个可信代理条目，接受 CIDR（10.0.0.0/8）或裸 IP（127.0.0.1）。
// 裸 IP 自动补 /32（IPv4）或 /128（IPv6）。返回 *net.IPNet。
func ParseTrustedProxy(s string) (*net.IPNet, error) {
	if _, ipNet, err := net.ParseCIDR(s); err == nil {
		return ipNet, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, &strconv.NumError{Func: "ParseTrustedProxy", Num: s, Err: strconv.ErrSyntax}
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}

// SetTrustedProxies 设置可信反代 IP/CIDR 列表；启动时调用一次，线程安全。
// 任一字符串非法则返回错误且不改变现有列表。
func SetTrustedProxies(list []string) error {
	nets := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := ParseTrustedProxy(s)
		if err != nil {
			return err
		}
		nets = append(nets, n)
	}
	trustedMu.Lock()
	trustedNets = nets
	trustedMu.Unlock()
	return nil
}

// isTrustedProxy 检查 remoteIP 是否属于可信代理列表
func isTrustedProxy(remoteIP string) bool {
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}
	trustedMu.RLock()
	defer trustedMu.RUnlock()
	for _, n := range trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP 提取客户端真实 IP。
//
// 安全策略（修复 P0-2 IP 伪造漏洞）：
//   - 仅当 RemoteAddr 属于可信代理列表时，才读取 X-Forwarded-For / X-Real-IP 转发头
//   - X-Forwarded-For 从右向左跳过所有可信代理，取第一个非可信 IP（防止代理链中注入）
//   - 可信代理为空 → 完全不信任转发头，直接返回 RemoteAddr（最安全默认）
//
// 代理链处理逻辑示例（proxies=[10.0.0.1, cdn_cidr]）：
//
//	X-Forwarded-For: "真实用户, 伪造IP, CDN节点, Nginx"  RemoteAddr=Nginx(可信)
//	→ 从右向左跳 Nginx → 跳 CDN节点 → 取 伪造IP 的左边 真实用户 ✓
func ClientIP(r *http.Request) string {
	// 1) 先取 RemoteAddr 主机部分
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}

	// 2) RemoteAddr 不是可信代理 → 直接返回，完全忽略转发头（避免伪造）
	if !isTrustedProxy(remoteHost) {
		return remoteHost
	}

	// 3) RemoteAddr 可信 → 按标准处理转发头
	// 优先 X-Forwarded-For，从右向左跳过可信代理（处理多级反代链）
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// 从右向左遍历，跳过列表中所有属于可信代理的条目
		for i := len(parts) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(parts[i])
			if ip == "" {
				continue
			}
			if !isTrustedProxy(ip) {
				return ip
			}
		}
		// 所有条目都可信 → 返回最左边（即第一个），退化到旧行为
		if len(parts) > 0 {
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
	}

	// 4) 次选 X-Real-IP
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}

	// 5) 退化：RemoteAddr
	return remoteHost
}

// QueryInt 解析查询参数为 int，缺失或非法时返回默认值
func QueryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// QueryStr 读取查询参数并去首尾空格
func QueryStr(r *http.Request, key, def string) string {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	return v
}
