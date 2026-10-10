package webx

import (
	"context"
	"net/http"

	"github.com/haozing/ploykit/platform/ids"
)

// HeaderRequestID 是贯穿请求-响应-日志的请求标识头。
const HeaderRequestID = "X-Request-Id"

type requestIDKey struct{}

// RequestIDFromCtx 取出中间件注入的请求标识；未经中间件处理时返回 ""。
func RequestIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDMiddleware 为每个请求生成/透传 X-Request-Id：入站已带则透传，
// 否则用 uuid v7 生成；一律写回响应头并注入 ctx，供 AccessLog 与业务日志关联。
// 入站头视为不可信输入，只做透传不做校验——追踪标识被伪造的危害限于日志污染。
//
// 命名说明：包内既有读头函数 RequestID(r)，中间件以 RequestIDMiddleware 区分。
// 链上顺序：RequestIDMiddleware 应在 AccessLog 外层，日志才能取到注入值。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if id == "" {
			id = ids.NewV7().String()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}
