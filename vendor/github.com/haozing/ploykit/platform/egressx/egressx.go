package egressx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
)

// blockedPrivate 是内建拒绝段全集（EG2，2026-10-07 扩容）：IPv4 15 段 +
// IPv6 15 段，对齐 arkadiyt/ssrf_filter 的 IPV4/IPV6_BLACKLIST 全集，另加
// NAT64 本地段 64:ff9b:1::/48 整段拒（ssrf_filter 经 ipv4_from_rfc6052 运行时
// 解码处理，ploykit 按 benchmark §一裁决先整段拒）。司法证据：Gitea 因黑名单
// 不全吃 CVE-2026-22874（CVSS 9.6）——"9 段不够"（docs/archive/2026-10/oss-benchmark-2026-10-07.md
// §一 / docs/archive/2026-10/platform-four-audit-2026-10-07.md EG2）。
//
// 每个 IPv4 段的内嵌编码变体（IPv4-mapped/compatible/translated/NAT64）不逐段
// 入表：判定入口（checkIP）先 Unmap 再解码内嵌载荷，按同一张 v4 表复查——
// ssrf_filter "变体全部入表"语义的 Go 等价实现。
var blockedPrivate = []netip.Prefix{
	// ---- IPv4（15 段，顺序与 ssrf_filter 一致） ----
	mustPrefix("0.0.0.0/8"),          // 本网络（仅源地址合法）
	mustPrefix("10.0.0.0/8"),         // RFC 1918 私网
	mustPrefix("100.64.0.0/10"),      // RFC 6598 CGNAT（Tailscale 等覆盖网默认段）
	mustPrefix("127.0.0.0/8"),        // 环回
	mustPrefix("169.254.0.0/16"),     // link-local（云元数据 169.254.169.254）
	mustPrefix("172.16.0.0/12"),      // RFC 1918 私网
	mustPrefix("192.0.0.0/24"),       // IETF 协议指派段
	mustPrefix("192.0.2.0/24"),       // TEST-NET-1 文档段
	mustPrefix("192.168.0.0/16"),     // RFC 1918 私网
	mustPrefix("198.18.0.0/15"),      // RFC 2544 基准测试段
	mustPrefix("198.51.100.0/24"),    // TEST-NET-2 文档段
	mustPrefix("203.0.113.0/24"),     // TEST-NET-3 文档段
	mustPrefix("224.0.0.0/4"),        // 组播（SSDP 239.255.255.250 探内网 UPnP 经典向量）
	mustPrefix("240.0.0.0/4"),        // 保留（原 E 类）
	mustPrefix("255.255.255.255/32"), // 广播（已含于 240/4；单列以对齐 ssrf_filter 全集）
	// ---- IPv6（15 段 ssrf_filter 全集 + NAT64 本地段） ----
	mustPrefix("::/128"),         // 未指定地址
	mustPrefix("::1/128"),        // 环回
	mustPrefix("64:ff9b:1::/48"), // RFC 8215 本地 NAT64（u 位布局，整段拒）
	mustPrefix("100::/64"),       // RFC 6666 discard-only
	mustPrefix("100:0:0:1::/64"), // RFC 9780 dummy 前缀
	mustPrefix("2001::/32"),      // Teredo 隧道
	mustPrefix("2001:2::/48"),    // RFC 5180 基准
	mustPrefix("2001:10::/28"),   // 弃用（原 ORCHID）
	mustPrefix("2001:20::/28"),   // ORCHIDv2
	mustPrefix("2001:db8::/32"),  // 文档段
	mustPrefix("2002::/16"),      // 6to4
	mustPrefix("3fff::/20"),      // RFC 9637 文档段
	mustPrefix("5f00::/16"),      // RFC 9602 SRv6 SID
	mustPrefix("fc00::/7"),       // 唯一本地（ULA）
	mustPrefix("fe80::/10"),      // link-local
	mustPrefix("ff00::/8"),       // 组播
}

// nat64WellKnown 是 RFC 6052 知名 NAT64 前缀 64:ff9b::/96：低 32 位内嵌
// IPv4，命中时须解码载荷按 v4 表复查（ssrf_filter 的 ipv4_from_rfc6052 语义
// ——DNS64 解析器可把私网 IPv4 嵌进"公网形态"IPv6 绕过 v4 判定）。载荷为
// 公网的 NAT64 地址是网关合法形态，故不整段入拒绝表。
var nat64WellKnown = mustPrefix("64:ff9b::/96")

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic("egressx: invalid built-in CIDR " + s)
	}
	return p
}

// BlockedCIDRs 返回内建拒绝段全集（字符串 CIDR，拷贝）。清单对齐
// arkadiyt/ssrf_filter 的 15+15 段 + NAT64 本地段整段拒；NAT64 知名前缀
// 64:ff9b::/96 不在其中——命中时解码内嵌 IPv4 按本表 v4 段复查。
func BlockedCIDRs() []string { return prefixStrings(blockedPrivate) }

// PrivateAllowCIDRs 返回内建拒绝段的镜像放行清单（拷贝）——本地开发态
// 私网逃生舱（WEBHOOK_ALLOW_PRIVATE_TARGET / FED_ALLOW_PRIVATE_IDP 开关）
// 的装配输入（NewGuard 的 extraAllowCIDRs）。与 BlockedCIDRs 同源自导出，
// 拒绝段扩容即自动跟随，webhooks/oidcfed 不再持有手工同步副本（EG2 三份
// 清单收敛单一真源）。注意：这会放行全部拒绝段（含组播/文档段），仅限
// 本地开发；绝不放入 0.0.0.0/0 之类的通配段（EG4 脚枪）。
func PrivateAllowCIDRs() []string { return prefixStrings(blockedPrivate) }

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

type Guard struct {
	extraAllow []netip.Prefix
}

func NewGuard(extraAllowCIDRs []string) (*Guard, error) {
	g := &Guard{}
	for _, c := range extraAllowCIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("egressx: ENGINE_EGRESS_EXTRA_ALLOW_CIDRS invalid CIDR %q: %w", c, err)
		}
		g.extraAllow = append(g.extraAllow, p)
	}
	return g, nil
}

func DefaultGuard() *Guard { return &Guard{} }

// ValidateURL 对出站目标 URL 做预检：scheme 白名单（https 公网放行；明文
// http 仅限 AllowCIDRs 例外段，ADR 0007 = docs/adr/0007-egress-single-exit.md）
// + host 私网/保留地址判定（IP 字面量直接判，域名解析后逐 IP 复查）。
//
// DNS 失败口径（EG3，2026-10-07 统一）：https 与 http 两分支同为
// **fail-closed**——域名解析失败/空结果一律返回错误（文案区分"temporarily
// unresolvable"），不再放行待 Dial 兜底。对齐 ssrf_filter/GitLab/Svix 主流
// 口径；唯一 fail-open 的 Metabase 有连接层强校验兜底（benchmark §一）。
// 取舍提示：GuardOf 消费方（如 webhooks deliverOne 对存量 URL 的"拒绝即
// 终态化"判定）在 DNS 抖动窗口内也会得到"拒绝"结论——接受该取舍的依据
// 与出路见 webhooks/app/service.go deliverOne 注释。
//
// Guard 构造后不可变，ValidateURL 并发安全。
func (g *Guard) ValidateURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("egress: URL parse failed: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("egress: outbound target missing host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		return g.httpWithinExtraAllow(ctx, host)
	default:
		return fmt.Errorf("egress: outbound target must be HTTPS (got %q)", u.Scheme)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return g.checkIP(ip.Unmap(), host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		// EG3：解析失败/空结果 fail-closed（与 http 分支统一；此前 fail-open）。
		return fmt.Errorf("egress: https target %q could not be resolved (temporarily unresolvable, fail-closed)", host)
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			return fmt.Errorf("egress: %q resolved to an invalid address %s", host, a.IP)
		}
		if err := g.checkIP(ip.Unmap(), host); err != nil {
			return err
		}
	}
	return nil
}

func (g *Guard) httpWithinExtraAllow(ctx context.Context, host string) error {
	contains := func(a netip.Addr) bool {
		for _, p := range g.extraAllow {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if contains(ip.Unmap()) {
			return nil
		}
		return fmt.Errorf("egress: http target %s does not hit the exception ranges (ADR 0007)", host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("egress: http target %q could not be resolved (fail-closed)", host)
	}
	for _, a := range addrs {
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok || !contains(ip.Unmap()) {
			return fmt.Errorf("egress: http target %q has address %s outside the exception ranges (ADR 0007)", host, a.IP)
		}
	}
	return nil
}

func (g *Guard) checkIP(a netip.Addr, host string) error {
	// EG1：netip.Prefix.Contains 对带 zone 的 IPv6 地址恒返回 false——不剥
	// zone，fe80::1%eth0 这类地址可穿透全部拒绝段（URL 预检与 Dialer 复核
	// 共用本入口）。
	if a.Zone() != "" {
		a = a.WithZone("")
	}
	// EG2：一切判定前归一——Unmap 处理 IPv4-mapped（::ffff:a.b.c.d）；内嵌
	// IPv4 形态（compatible/translated/NAT64）解码后按同一逻辑复查 v4 表。
	// 归一收在判定入口（而非仅靠各调用方），直接调 checkIP 的路径同样覆盖。
	a = a.Unmap()
	if v4, ok := unembedV4(a); ok {
		return g.checkIP(v4, host)
	}
	for _, p := range g.extraAllow {
		if p.Contains(a) {
			return nil
		}
	}
	for _, p := range blockedPrivate {
		if p.Contains(a) {
			return fmt.Errorf("egress: %q resolved to a private/reserved address %s (internal archive 07 §7.8 rejects)", host, a)
		}
	}
	return nil
}

// unembedV4 把"内嵌 IPv4"形态的 IPv6 地址解码为其 IPv4 载荷（EG2）：
//
//   - IPv4-mapped（::ffff:a.b.c.d，RFC 4291）——通常已被 Unmap 归一，此处兜底；
//   - IPv4-compatible（::a.b.c.d，弃用形态）——高 96 位全零；
//   - IPv4-translated（::ffff:0:a.b.c.d，RFC 2765）——bits 64-95 为 0xffff0000；
//   - NAT64 知名前缀（64:ff9b::a.b.c.d，RFC 6052）。
//
// 解码后按同一判定逻辑（allowlist 优先、再查 v4 拒绝段）复查——放行段
// 语义因此贯穿 NAT64 形态（本地开发态 NAT64 环境可用）。ok=false 表示
// 不是内嵌形态。解码出的 v4 是 Is4，不会二次进入本函数（无递归环）。
func unembedV4(a netip.Addr) (netip.Addr, bool) {
	if a.Is4() {
		return netip.Addr{}, false
	}
	b := a.As16()
	lo := [4]byte{b[12], b[13], b[14], b[15]}
	hi := b[:12]
	compatible := allZero(hi)
	translated := allZero(hi[:8]) && hi[8] == 0xff && hi[9] == 0xff && hi[10] == 0 && hi[11] == 0
	if compatible || translated || nat64WellKnown.Contains(a) {
		return netip.AddrFrom4(lo), true
	}
	return netip.Addr{}, false
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
