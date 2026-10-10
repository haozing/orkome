package egressx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// 受控出站 HTTP client（ADR 0007，docs/adr/0007-egress-single-exit.md：
// egressx 受控 client 是框架出站的唯一通道；capability-impl-plan §五）。
// 两层防护：
//
//  1. Transport 内 RoundTrip 前对完整 URL 跑 Guard.ValidateURL 预检——
//     scheme（https 公网放行；明文 http 仅限 AllowCIDRs 放行段，ADR 0007）、
//     host 私网/环回/link-local/未指定地址判定（IP 字面量直接判，域名解析后
//     逐 IP 复查）；
//  2. net.Dialer.Control——DNS 解析后、TCP 握手前对"实际要连的 IP"再跑同套
//     判定。这是防 DNS rebinding 的关键一层：预检时域名解析到公网、连接时
//     解析到私网的 TOCTOU 绕过，会在 Control 处被拦下（Control 只看 IP，
//     与 URL host 的名字无关）。
//
// 重定向由 http.Client 逐跳经同一 Transport，每一跳都重新过检。
// 注意：Transport 沿用 http.ProxyFromEnvironment——设置了 HTTP(S)_PROXY 时
// Control 复核的是代理的实际地址（本地代理需在 AllowCIDRs 放行环回段）。

// defaultHTTPTimeout 是 Opts.Timeout 未设（<=0）时的整体请求超时默认值。
const defaultHTTPTimeout = 30 * time.Second

// Opts 配置 NewHTTPClient。
type Opts struct {
	// Timeout 是整体请求超时（http.Client.Timeout）。<=0 时取 30s 默认，
	// 防漏配产品悬死出站请求。
	Timeout time.Duration
	// AllowCIDRs 是显式放行网段（如本区域内网服务、本地开发接收端），
	// 优先于内建拒绝段（blockedPrivate）生效；网段非法时构造报错。
	AllowCIDRs []string
}

// NewHTTPClient 构建受控出站 HTTP client。
func NewHTTPClient(opts Opts) (*http.Client, error) {
	guard, err := NewGuard(opts.AllowCIDRs)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   controlHook(guard),
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &guardTransport{base: transport, guard: guard},
	}, nil
}

// guardTransport 在每个出站请求（含重定向的每一跳）前跑 URL 预检。
type guardTransport struct {
	base  http.RoundTripper
	guard *Guard
}

func (t *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.guard.ValidateURL(req.Context(), req.URL.String()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// GuardOf 返回 client 内嵌的出站判定器；c 不是 NewHTTPClient 构造（或被
// 再包装过 Transport）时返回 nil。调用方可据此在请求前做快速预检——
// 例如 webhooks 对存量 URL 的"拒绝即终态化"判定，与 client 防护同源。
func GuardOf(c *http.Client) *Guard {
	if c == nil {
		return nil
	}
	if t, ok := c.Transport.(*guardTransport); ok {
		return t.guard
	}
	return nil
}

// controlHook 返回 Dialer.Control 回调：TCP 握手前对实际连接地址复核。
func controlHook(guard *Guard) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if err := guard.checkDialAddress(address); err != nil {
			return fmt.Errorf("egress: dial %s %s rejected: %w", network, address, err)
		}
		return nil
	}
}

// checkDialAddress 是 Control 回调的判定核（单测入口）：address 形如
// "ip:port" 或 "[ipv6]:port"。allowlist 命中放行，否则跑内建拒绝段判定；
// 非 IP 字面量（不应出现——dialer 拿到的必是解析后的地址）一律 fail-closed。
func (g *Guard) checkDialAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("egress: dial address %q: %w", address, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("egress: dial address %q is not an IP literal: %w", address, err)
	}
	return g.checkIP(ip.Unmap(), host)
}
