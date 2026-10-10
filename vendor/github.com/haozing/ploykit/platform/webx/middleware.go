package webx

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

// Recover 兜住 handler panic，记日志并返回 500，不让连接裸断。
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// W6：panic 值不含位置信息，附带堆栈才能定位 500 的源头。
					log.Error("panic recovered", "path", r.URL.Path, "panic", rec,
						"stack", string(debug.Stack()))
					WriteError(w, http.StatusInternalServerError, "E_INTERNAL", "internal error", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack 透传给底层 ResponseWriter，保证 WebSocket 升级在录音层之下仍可用。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("webx: underlying ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Unwrap 透传底层 ResponseWriter（W10）：http.ResponseController（Go 1.20+）
// 经此通路直达底层 writer 的 Flush / NewEventSource 等能力——SSE/流式响应
// 经过 AccessLog 时不再因 recorder 未实现 http.Flusher 而静默退化。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// AccessLog 按状态码分级记录每个请求：方法/路径/状态/耗时/IP/用户/客户端元数据。
func AccessLog(log *slog.Logger, trustedProxies ...*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}
			// W11：user_id 优先读 holder cell——AccessLog 位于 Authenticate
			// 外层，原 request 的 ctx 永远没有内层派生的 Principal（user_id
			// 恒空的根因）；链上挂了 PrincipalHolder 时由 Authenticate 顺带
			// 填充。cell 缺位回落 W11 之前的 ctx 读取（holder 缺位行为不变）。
			user := ""
			if cell := principalCellFrom(r.Context()); cell != nil {
				if p := cell.Get(); p != nil {
					user = p.UserID
				}
			} else if p := PrincipalFrom(r.Context()); p != nil {
				user = p.UserID
			}
			clientPlatform, clientVersion, clientOS := ClientMetadataFrom(r.Context())
			// request_id 依赖链上外层的 RequestIDMiddleware 注入 ctx；缺失时为空串。
			log.Log(r.Context(), level, "http",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"ms", time.Since(start).Milliseconds(), "ip", ClientIP(r, trustedProxies...), "user_id", user,
				"request_id", RequestIDFromCtx(r.Context()),
				"client_platform", clientPlatform, "client_version", clientVersion, "client_os", clientOS)
		})
	}
}

// SecurityHeaders 设置基础防嗅探/防点击劫持/引宿收紧响应头。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
