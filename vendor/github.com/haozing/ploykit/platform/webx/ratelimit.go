package webx

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Limiter 是限流后端的抽象。Allow 返回是否放行以及建议的重试等待。
type Limiter interface {
	Allow(ctx context.Context, key string, perMinute int) (ok bool, retryAfter time.Duration)
}

// FailOpenLimiter 永远放行，供未部署限流后端（如本地开发）时兜底。
type FailOpenLimiter struct{}

func (FailOpenLimiter) Allow(context.Context, string, int) (bool, time.Duration) { return true, 0 }

// RateLimit 按 keyFn 提取的维度限流；超限返回 429 与 Retry-After。
func RateLimit(l Limiter, dimension string, perMinute int, keyFn func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "rl:" + dimension + ":" + keyFn(r)
			ok, retry := l.Allow(r.Context(), key, perMinute)
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds())+1, 1)))
				WriteError(w, http.StatusTooManyRequests, "E_RATE_LIMITED", "too many requests", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP 解析客户端真实 IP：仅当 RemoteAddr 命中可信代理网段时才采信 X-Forwarded-For，
// 且从右往左跳过仍属于可信代理的条目。
func ClientIP(r *http.Request, trustedProxies ...*net.IPNet) string {
	return extractIP(r, trustedProxies)
}

// ParseTrustedProxies 解析逗号分隔的 CIDR 列表，非法条目跳过并告警。
func ParseTrustedProxies(raw string) []*net.IPNet {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var nets []*net.IPNet
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(p)
		if err != nil {
			slog.Warn("webx: invalid trusted proxy CIDR, skipping", "cidr", p, "error", err)
			continue
		}
		nets = append(nets, cidr)
	}
	return nets
}

func extractIP(r *http.Request, trustedProxies []*net.IPNet) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	if len(trustedProxies) > 0 {
		if remoteIP := net.ParseIP(remoteHost); remoteIP != nil && isTrustedProxy(remoteIP, trustedProxies) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				for i := len(parts) - 1; i >= 0; i-- {
					if candidate := net.ParseIP(strings.TrimSpace(parts[i])); candidate != nil &&
						!isTrustedProxy(candidate, trustedProxies) {
						return candidate.String()
					}
				}
			}
		}
	}
	if ip := net.ParseIP(remoteHost); ip != nil {
		return ip.String()
	}
	return remoteHost
}

func isTrustedProxy(ip net.IP, cidrs []*net.IPNet) bool {
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}
