package metrics

import (
	"crypto/subtle"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler 返回 /metrics 端点的 promhttp 处理器，吐出本 Registry 私有
// 注册表里的全部指标：OTel 桥接指标 + go/process 运行时指标。
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{})
}

// HandlerWithToken 在 Handler 外包一层 token 门禁：
//   - token 为空 = 不设门禁（内网/本地部署形态），直接返回裸 Handler；
//   - 否则要求 Authorization: Bearer <token> 或 ?token=<token> 二选一，
//     两者都用常数时间比较，防止按位匹配的时序侧信道；
//   - 校验失败返回 404 而非 401——不向未授权方确认该端点存在。
func (r *Registry) HandlerWithToken(token string) http.Handler {
	h := r.Handler()
	if token == "" {
		return h
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !tokenMatches(req, want) {
			http.NotFound(w, req)
			return
		}
		h.ServeHTTP(w, req)
	})
}

// tokenMatches 依序尝试 Bearer 头与 query 参数两条凭据通道。
func tokenMatches(req *http.Request, want []byte) bool {
	if subtle.ConstantTimeCompare([]byte(bearerToken(req.Header.Get("Authorization"))), want) == 1 {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(req.URL.Query().Get("token")), want) == 1
}

// bearerToken 剥掉 Authorization 头的 Bearer 前缀；非 Bearer 形式
// （Basic 等）或只有前缀本身时返回空串，交给比较环节判负。
func bearerToken(auth string) string {
	const prefix = "Bearer "
	if len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return ""
}
