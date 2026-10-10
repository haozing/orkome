package webx

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Timeout 限制单个请求的处理时长：到点未完成且响应尚未开始写时，兜底写 504 E_TIMEOUT。
// handler 在独立 goroutine 中继续跑完（Go 无法强杀 goroutine），其后续写入被包装层丢弃；
// 请求 ctx 同步带超时，业务侧的慢查询可据此自行中断。
//
// 约束：不可包裹 /ws 升级与 SSE 路由——Hijack 后连接已脱离 net/http 的响应模型，
// 504 兜底对这类连接无效（写不进去或只会破坏流），请改用 TimeoutExcept 豁免这些
// 路径前缀，或在路由组装层把它们排除在本中间件之外。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveWithTimeout(w, r, next, d)
		})
	}
}

// TimeoutExcept 同 Timeout，但豁免指定路径前缀——/ws 升级与 SSE 等长连接路由不能
// 进入超时模型（见 Timeout 文档约束），此前每个产品都要手写豁免包装。豁免路径直接
// 透传（不设 ctx 超时、不启 504 兜底）。
func TimeoutExcept(d time.Duration, exemptPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range exemptPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			serveWithTimeout(w, r, next, d)
		})
	}
}

// serveWithTimeout 是 Timeout/TimeoutExcept 共用的超时执行体：设 ctx 超时、
// handler 移入独立 goroutine、到点未开写兜底 504。
func serveWithTimeout(w http.ResponseWriter, r *http.Request, next http.Handler, d time.Duration) {
	ctx, cancel := context.WithTimeout(r.Context(), d)
	defer cancel()
	r = r.WithContext(ctx)

	tw := &timeoutWriter{ResponseWriter: w}
	done := make(chan struct{})
	panicChan := make(chan any, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				panicChan <- p
			}
		}()
		next.ServeHTTP(tw, r)
		close(done)
	}()

	select {
	case p := <-panicChan:
		// 回到外层 goroutine 重新 panic，让 Recover/net/http 的兜底按原语义接住。
		panic(p)
	case <-done:
	case <-ctx.Done():
		tw.mu.Lock()
		alreadyWrote := tw.wrote
		tw.timedOut = true
		tw.mu.Unlock()
		if !alreadyWrote {
			WriteError(w, http.StatusGatewayTimeout, CodeTimeout, "request timeout", nil)
		}
	}
}

// timeoutWriter 只为防双写存在：wrote 标记响应是否已开始（首次 WriteHeader/Write），
// 超时后 timedOut 让 handler 的迟到写入静默丢弃，不再触达真实连接。
type timeoutWriter struct {
	http.ResponseWriter
	mu       sync.Mutex
	wrote    bool
	timedOut bool
}

func (t *timeoutWriter) WriteHeader(code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return
	}
	t.wrote = true
	t.ResponseWriter.WriteHeader(code)
}

func (t *timeoutWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	t.wrote = true
	return t.ResponseWriter.Write(p)
}
