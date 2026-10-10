package webx

import (
	"net/http"
)

// BodyLimit 限制请求体大小：只包 MaxBytesReader，不预读、不包装 ResponseWriter。
// 超限读出的 *http.MaxBytesError 由 DecodeJSON 统一翻译成 413 E_PAYLOAD_TOO_LARGE，
// 因此业务 handler 必须经 DecodeJSON 消费请求体才能拿到 413 语义。
func BodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
