package http

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"

	"orkome/internal/platform/webx"
)

type LayerLimit struct {
	Limit  int
	Window time.Duration
}

type RateLimitConfig struct {
	Global LayerLimit
	PerIP  LayerLimit
	AbsIP  LayerLimit
}

func DefaultWebhookRateConfig() RateLimitConfig {
	return RateLimitConfig{
		Global: LayerLimit{Limit: 600, Window: time.Minute},
		PerIP:  LayerLimit{Limit: 60, Window: time.Minute},
		AbsIP:  LayerLimit{Limit: 120, Window: time.Minute},
	}
}

type windowStore interface {
	allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration)
}

type memoryStore struct {
	mu    sync.Mutex
	hits  map[string][]time.Time
	swept time.Time
	nowFn func() time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{hits: map[string][]time.Time{}, swept: time.Now(), nowFn: time.Now}
}

func (m *memoryStore) allow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration) {
	now := m.nowFn()
	m.mu.Lock()
	defer m.mu.Unlock()

	times := m.hits[key]
	keep := times[:0]
	for _, t := range times {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= limit {
		m.hits[key] = keep

		return false, window - now.Sub(keep[0])
	}
	m.hits[key] = append(keep, now)
	m.sweepLocked(now, window)
	return true, 0
}

func (m *memoryStore) sweepLocked(now time.Time, window time.Duration) {
	if now.Sub(m.swept) < window {
		return
	}
	m.swept = now
	for k, times := range m.hits {
		dead := true
		for _, t := range times {
			if now.Sub(t) < window {
				dead = false
				break
			}
		}
		if dead {
			delete(m.hits, k)
		}
	}
}

type redisStore struct{ limiter *redis_rate.Limiter }

func newRedisStore(cli redis.UniversalClient) *redisStore {
	return &redisStore{limiter: redis_rate.NewLimiter(cli)}
}

func (s *redisStore) allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration) {
	res, err := s.limiter.Allow(ctx, key, redis_rate.Limit{Rate: limit, Period: window, Burst: limit})
	if err != nil {
		return true, 0
	}
	return res.Allowed > 0, res.RetryAfter
}

type WebhookRateLimiter struct {
	cfg     RateLimitConfig
	mem     *memoryStore
	rds     windowStore
	trusted []netip.Prefix
}

func NewWebhookRateLimiter(redis redis.UniversalClient, cfg RateLimitConfig, trusted []netip.Prefix) *WebhookRateLimiter {
	l := &WebhookRateLimiter{cfg: cfg, mem: newMemoryStore(), trusted: trusted}
	if redis != nil {
		l.rds = newRedisStore(redis)
	}
	return l
}

func (l *WebhookRateLimiter) Allow(ctx context.Context, r *http.Request) (bool, time.Duration) {
	layers := []struct {
		key string
		cfg LayerLimit
	}{
		{"webhook:global", l.cfg.Global},
		{"webhook:ip:" + ClientIP(r, l.trusted), l.cfg.PerIP},
		{"webhook:abs:" + RemoteAddrHost(r), l.cfg.AbsIP},
	}
	for _, ly := range layers {
		if ly.cfg.Limit <= 0 || ly.cfg.Window <= 0 {
			continue
		}
		ok, retry := l.mem.allow(ctx, ly.key, ly.cfg.Limit, ly.cfg.Window)
		if !ok {
			return false, retry
		}
		if l.rds != nil {
			ok, retry = l.rds.allow(ctx, ly.key, ly.cfg.Limit, ly.cfg.Window)
			if !ok {
				return false, retry
			}
		}
	}
	return true, 0
}

func WriteRateLimited(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds())+1, 1)))
	webx.WriteError(w, http.StatusTooManyRequests, "E_RATE_LIMITED", "too many requests", nil)
}

func ParseTrustedProxies(raw string) []netip.Prefix {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []netip.Prefix
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(p); err == nil {
			out = append(out, prefix)
		}
	}
	return out
}

func RemoteAddrHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	remote := RemoteAddrHost(r)
	rip := net.ParseIP(remote)
	if rip == nil || len(trusted) == 0 || !containsPrefix(trusted, rip) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		cand := net.ParseIP(strings.TrimSpace(parts[i]))
		if cand != nil && !containsPrefix(trusted, cand) {
			return cand.String()
		}
	}
	return remote
}

func containsPrefix(ps []netip.Prefix, ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
