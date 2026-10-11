package adapters

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStringCache is a thin redis-backed string cache (Get/Set/Del).
// The framework's redisx.StringCache was removed in ploykit v0.4.0; this local
// copy keeps orkome's PAT-resolution cache semantics unchanged.
type RedisStringCache struct{ C redis.UniversalClient }

func (s RedisStringCache) Get(ctx context.Context, key string) (string, bool) {
	val, err := s.C.Get(ctx, key).Result()
	return val, err == nil && val != ""
}

func (s RedisStringCache) Set(ctx context.Context, key, val string, ttl time.Duration) {
	_ = s.C.Set(ctx, key, val, ttl).Err()
}

func (s RedisStringCache) Del(ctx context.Context, keys ...string) {
	if len(keys) > 0 {
		_ = s.C.Del(ctx, keys...).Err()
	}
}
