package redisx

import (
	"context"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/haozing/ploykit/platform/webx"
	"github.com/redis/go-redis/v9"
)

// GCRALimiter 基于 Redis 的 GCRA 限流，实现 webx.Limiter。
// 放在 redisx 而非 webx，避免 webx 对 Redis 产生硬依赖。
type GCRALimiter struct {
	limiter *redis_rate.Limiter
}

func NewGCRALimiter(cli redis.UniversalClient) *GCRALimiter {
	return &GCRALimiter{limiter: redis_rate.NewLimiter(cli)}
}

func (g *GCRALimiter) Allow(ctx context.Context, key string, perMinute int) (bool, time.Duration) {
	res, err := g.limiter.Allow(ctx, key, redis_rate.PerMinute(perMinute))
	if err != nil {
		// Redis 故障时放行：限流是保护手段，不能成为可用性单点。
		return true, 0
	}
	return res.Allowed > 0, res.RetryAfter
}

var _ webx.Limiter = (*GCRALimiter)(nil)
