package webx

import (
	"net/http"
	"strings"
)

// ParseOrigins 解析逗号分隔的 Origin 列表。
func ParseOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CORS 白名单放行跨域请求；allowCredentials 时禁用通配符回退为精确回显。
func CORS(allowedOrigins []string, allowCredentials bool) func(http.Handler) http.Handler {
	wildcard := false
	set := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			wildcard = true
			continue
		}
		set[o] = true
	}
	return func(next http.Handler) http.Handler {
		if len(allowedOrigins) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := origin != "" && (wildcard || set[origin])
			if !allowed {
				// 不在白名单：预检直接拒绝，普通请求放行但不带 CORS 头。
				// W8：放行路径也声明 Vary: Origin——本中间件的行为依赖 Origin
				// 取值，共享缓存若按 URL 缓存无 CORS 头的响应，会把它喂给
				// 白名单 Origin（拿到缺 Access-Control-Allow-Origin 的响应）。
				w.Header().Add("Vary", "Origin")
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			// W8：跨域场景 JS 默认读不到自定义响应头，显式暴露 X-Request-Id
			//（与 AccessLog/业务日志的关联标识）——同源部署无碍，前后端
			// 分离部署时必需。
			h.Set("Access-Control-Expose-Headers", HeaderRequestID)
			if wildcard && !allowCredentials {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if allowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if m := r.Header.Get("Access-Control-Request-Method"); m != "" {
					h.Set("Access-Control-Allow-Methods", m)
				}
				if hdrs := r.Header.Get("Access-Control-Request-Headers"); hdrs != "" {
					h.Set("Access-Control-Allow-Headers", hdrs)
				}
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
