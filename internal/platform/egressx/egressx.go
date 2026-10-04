package egressx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
)

var blockedPrivate = []netip.Prefix{
	mustPrefix("127.0.0.0/8"),
	mustPrefix("10.0.0.0/8"),
	mustPrefix("172.16.0.0/12"),
	mustPrefix("192.168.0.0/16"),
	mustPrefix("169.254.0.0/16"),
	mustPrefix("0.0.0.0/8"),
	mustPrefix("::1/128"),
	mustPrefix("fc00::/7"),
	mustPrefix("fe80::/10"),
}

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic("egressx: invalid built-in CIDR " + s)
	}
	return p
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
		return nil
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

	for _, p := range g.extraAllow {
		if p.Contains(a) {
			return nil
		}
	}
	for _, p := range blockedPrivate {
		if p.Contains(a) {
			return fmt.Errorf("egress: %q resolved to a private address %s (internal archive 07 §7.8 rejects)", host, a)
		}
	}
	return nil
}
