package webx

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

// CheckFunc 是一项健康检查：返回 nil 视为通过。
type CheckFunc func(ctx context.Context) error

// Health 聚合 Readiness 检查项。Liveness 恒 200（进程活着即可，不探下游），
// Readiness 要求全部检查通过——失败时 503，供负载均衡摘除流量。
type Health struct {
	mu     sync.RWMutex
	checks map[string]CheckFunc
}

func NewHealth() *Health {
	return &Health{checks: map[string]CheckFunc{}}
}

// AddCheck 注册一项检查；同名重复注册以后注册的为准。链式返回自身。
func (h *Health) AddCheck(name string, fn CheckFunc) *Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
	return h
}

// Liveness 存活探针：不依赖任何下游，进程能响应即 200。
func (h *Health) Liveness() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Readiness 就绪探针：全部检查并发执行，任一失败 503 并按名称排序列出失败项；
// 单项检查 2s 超时，超时计为失败——避免慢下游把探针拖死成级联超时。
func (h *Health) Readiness() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		checks := make(map[string]CheckFunc, len(h.checks))
		for name, fn := range h.checks {
			checks[name] = fn
		}
		h.mu.RUnlock()

		var (
			wg     sync.WaitGroup
			mu     sync.Mutex
			failed []string
		)
		for name, fn := range checks {
			wg.Add(1)
			go func(name string, fn CheckFunc) {
				defer wg.Done()
				// 检查 goroutine 与 2s 预算并行：超时后结果被放弃，不再等待其返回。
				errc := make(chan error, 1)
				ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				go func() { errc <- fn(ctx) }()
				var err error
				select {
				case err = <-errc:
				case <-ctx.Done():
					err = ctx.Err()
				}
				cancel()
				if err != nil {
					mu.Lock()
					failed = append(failed, name)
					mu.Unlock()
				}
			}(name, fn)
		}
		wg.Wait()

		if len(failed) > 0 {
			sort.Strings(failed)
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "failed": failed})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
