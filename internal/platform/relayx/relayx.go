package relayx

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"orkome/internal/contract/wswire"
)

type Mode string

const (
	ModeMemory Mode = "memory"

	ModeSharded Mode = "sharded"

	ModeMirrored Mode = "mirrored"
)

func ParseMode(v string, hasRedis bool) (Mode, error) {
	switch Mode(strings.TrimSpace(strings.ToLower(v))) {
	case "":
		if hasRedis {
			return ModeSharded, nil
		}
		return ModeMemory, nil
	case ModeMemory:
		return ModeMemory, nil
	case ModeSharded:
		if !hasRedis {
			return "", fmt.Errorf("REALTIME_RELAY_MODE=sharded requires REDIS_URL (internal archive 02 §11: memory only without Redis)")
		}
		return ModeSharded, nil
	case ModeMirrored:
		if !hasRedis {
			return "", fmt.Errorf("REALTIME_RELAY_MODE=mirrored requires REDIS_URL (internal archive 02 §11: memory only without Redis)")
		}
		return ModeMirrored, nil
	default:
		return "", fmt.Errorf("REALTIME_RELAY_MODE invalid value %q (memory/sharded/mirrored)", v)
	}
}

type Relay struct {
	RDB    redis.UniversalClient
	Prefix string
	Shards int
	Hub    Broadcaster
	Log    *slog.Logger

	Mode Mode

	ReplayWindow time.Duration

	MaxLen int64
	lastID map[int]string
}

func (r *Relay) mode() Mode {
	if r.Mode == "" {
		return ModeSharded
	}
	return r.Mode
}

func (r *Relay) Fanout(ctx context.Context, scope string, frame wswire.Frame, local func()) error {
	m := r.mode()
	if m == ModeMemory || r.RDB == nil {
		local()
		return nil
	}
	if m == ModeMirrored {
		local()
	}
	return r.Publish(ctx, scope, frame)
}

type Broadcaster interface {
	Broadcast(ctx context.Context, scope string, frame wswire.Frame)
}

const fallbackLastID = "0"

func (r *Relay) shardOf(scope string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(scope))
	return int(h.Sum32() % uint32(r.shards()))
}

func (r *Relay) shards() int {
	if r.Shards <= 0 {
		return 8
	}
	return r.Shards
}

func (r *Relay) stream(shard int) string {
	p := r.Prefix
	if p == "" {
		p = "orkome:relay"
	}
	return fmt.Sprintf("%s:%d", p, shard)
}

func (r *Relay) Publish(ctx context.Context, scope string, frame wswire.Frame) error {
	data, err := marshalFrame(scope, frame)
	if err != nil {
		return err
	}
	maxLen := r.MaxLen
	if maxLen <= 0 {
		maxLen = 10000
	}
	return r.RDB.XAdd(ctx, &redis.XAddArgs{
		Stream: r.stream(r.shardOf(scope)),
		MaxLen: maxLen, Approx: true,
		Values: map[string]any{"d": data},
	}).Err()
}

func (r *Relay) Run(ctx context.Context) error {
	if r.RDB == nil {
		return nil
	}
	if r.ReplayWindow <= 0 {
		r.ReplayWindow = 5 * time.Minute
	}
	r.lastID = make(map[int]string, r.shards())

	cutoff := time.Now().Add(-r.ReplayWindow).UTC()
	for i := 0; i < r.shards(); i++ {
		entries, err := r.RDB.XRange(ctx, r.stream(i), "-", "+").Result()
		if err != nil {
			continue
		}
		id := fallbackLastID
		for k, e := range entries {
			ts, _ := parseXID(e.ID)
			if ts.After(cutoff) {
				if k > 0 {
					id = entries[k-1].ID
				}
				break
			}
			id = e.ID
		}
		r.lastID[i] = id
	}

	streams := make([]string, r.shards())
	ids := make([]string, r.shards())
	for {
		for i := range streams {
			streams[i] = r.stream(i)
			if r.lastID[i] == "" {
				r.lastID[i] = fallbackLastID
			}
			ids[i] = r.lastID[i]
		}

		req := make([]string, 0, len(streams)*2)
		req = append(req, streams...)
		req = append(req, ids...)
		res, err := r.RDB.XRead(ctx, &redis.XReadArgs{
			Streams: req, Block: 5 * time.Second, Count: 64,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			if r.Log != nil {
				r.Log.Error("relayx read", "err", err)
			}
			time.Sleep(time.Second)
			continue
		}
		for _, s := range res {
			var shard int
			fmt.Sscanf(s.Stream, r.Prefix+":%d", &shard)
			if r.Prefix == "" {
				fmt.Sscanf(s.Stream, "orkome:relay:%d", &shard)
			}
			for _, e := range s.Messages {
				r.lastID[shard] = e.ID
				if data, ok := e.Values["d"].(string); ok {
					if scope, frame, ok := unmarshalFrame(data); ok {
						r.Hub.Broadcast(ctx, scope, frame)
					}
				}
			}
		}
	}
}

func parseXID(id string) (time.Time, bool) {
	var ms int64
	if _, err := fmt.Sscanf(id, "%d-", &ms); err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func marshalFrame(scope string, f wswire.Frame) (string, error) {
	b, err := json.Marshal(struct {
		Scope string       `json:"scope"`
		Frame wswire.Frame `json:"frame"`
	}{scope, f})
	return string(b), err
}

func unmarshalFrame(data string) (string, wswire.Frame, bool) {
	var x struct {
		Scope string       `json:"scope"`
		Frame wswire.Frame `json:"frame"`
	}
	if json.Unmarshal([]byte(data), &x) != nil || x.Scope == "" {
		return "", wswire.Frame{}, false
	}
	return x.Scope, x.Frame, true
}
