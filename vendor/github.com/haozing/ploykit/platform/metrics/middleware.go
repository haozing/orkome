package metrics

import (
	"net/http"
	"path"
	"strings"
	"time"
)

// HTTP 请求中间件（capability-impl-plan §四 MVP 面）：请求计数 + 时延直方图，
// 按 method + 规范化路由模板分组。中间件挂在 metrics 包内而不进 webx——
// 观测是可选件，webx 不为它新增 prometheus 依赖（Kratos"核心不背可选依赖"）。

const (
	// MetricHTTPRequests 计数仪器名；prometheus 导出面自动补 _total 后缀，
	// 即 ploykit_http_requests_total{method,route}。
	MetricHTTPRequests = "ploykit_http_requests"
	// MetricHTTPDurationSeconds 时延直方图仪器名，导出面为
	// ploykit_http_request_duration_seconds_{bucket,count,sum}{method,route}。
	MetricHTTPDurationSeconds = "ploykit_http_request_duration_seconds"

	labelMethod = "method"
	labelRoute  = "route"

	// routeFallback 是路由模板基数超出 maxRoutes 后的兜底标签值。
	routeFallback = "other"
	// maxRoutes 限制 route 标签的基数上限：折叠启发式（见 routeTemplate）
	// 不能完美识别所有值形态段，这里保证标签集有硬上界，防御性兜底。
	maxRoutes = 512
)

// DefaultDurationBuckets 是请求时延直方图的秒级默认桶（prometheus
// defBuckets 同款）。OTel 默认桶以毫秒量级为主，对秒级 HTTP 时延分辨力不足，
// NewMount 内建 Registry 时自动应用（opts 显式定制同名桶时优先）。
var DefaultDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Middleware 给 next 包上请求计数与时延观测。route 标签取路由模板而非实参
// 值，防标签爆炸（见 routeTemplate）；关闭态 Mount（reg 为 nil）直通 next。
func (m *Mount) Middleware(next http.Handler) http.Handler {
	if m.reg == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		route := m.routeLabel(req)
		start := time.Now()
		defer func() {
			attrs := map[string]string{labelMethod: req.Method, labelRoute: route}
			m.reg.IncrAttrs(MetricHTTPRequests, attrs)
			m.reg.ObserveAttrs(MetricHTTPDurationSeconds, time.Since(start).Seconds(), attrs)
		}()
		next.ServeHTTP(w, req)
	})
}

// routeLabel 在模板外再加一层基数硬上限：见过的模板放行，超过 maxRoutes 个
// 之后新模板统一折叠为 "other"。
func (m *Mount) routeLabel(req *http.Request) string {
	route := routeTemplate(req)
	m.seenMu.Lock()
	defer m.seenMu.Unlock()
	if m.seen == nil {
		m.seen = make(map[string]struct{})
	}
	if _, ok := m.seen[route]; ok || len(m.seen) < maxRoutes {
		m.seen[route] = struct{}{}
		return route
	}
	return routeFallback
}

// routeTemplate 提取路由模板标签：
//   - 优先取 Go 1.22+ ServeMux 的匹配模板（req.Pattern，形如 "/api/tasks/{id}"）。
//     注意它只在"本中间件位于 mux 匹配之后"（mux.Handle(pattern, mw(h)) 形态）
//     才有值——把中间件包在 mux 外层时匹配尚未发生，Pattern 为空；
//   - 否则对 URL path 做"值段折叠"：把纯数字 / UUID / 长十六进制 / 长混合
//     字母数字的段替换为 {id}，其余段保留。启发式不可能覆盖所有 ID 形态，
//     残余基数由 maxRoutes 上限（"other"）兜底。
func routeTemplate(req *http.Request) string {
	if p := req.Pattern; p != "" {
		return p
	}
	return normalizePath(req.URL.Path)
}

// normalizePath 清理 path 并折叠值形态段。
func normalizePath(p string) string {
	c := path.Clean("/" + p)
	if c == "/" {
		return "/"
	}
	segs := strings.Split(c[1:], "/")
	for i, s := range segs {
		if looksLikeValue(s) {
			segs[i] = "{id}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// looksLikeValue 判定段是否"值形态"（ID/哈希/时间戳等高基数内容）：
// 纯数字、UUID（带/不带连字符）、>=8 位十六进制、>=16 位含字母且含数字的
// 字母数字混合串（nanoid/ULID/snowflake 等）。
func looksLikeValue(s string) bool {
	if isDigits(s) {
		return true
	}
	if isUUID(s) {
		return true
	}
	if len(s) >= 8 && isHex(s) {
		return true
	}
	return len(s) >= 16 && isAlnum(s) && hasAlpha(s) && hasDigit(s)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return s != ""
}

func isAlnum(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return s != ""
}

func hasAlpha(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func hasDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// isUUID 判定 8-4-4-4-12 十六进制形态（含整串 32 hex 无连字符变体）。
func isUUID(s string) bool {
	if len(s) == 36 {
		return isHex(s[0:8]) && s[8] == '-' && isHex(s[9:13]) && s[13] == '-' &&
			isHex(s[14:18]) && s[18] == '-' && isHex(s[19:23]) && s[23] == '-' && isHex(s[24:36])
	}
	return len(s) == 32 && isHex(s)
}
