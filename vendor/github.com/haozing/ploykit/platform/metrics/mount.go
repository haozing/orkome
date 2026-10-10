package metrics

import (
	"net/http"
	"sync"
)

// 本文件是"可挂载"装配面（capability-impl-plan §四 + migration §五 Q 合并验收：
// Handler() / Middleware() / Config{Enabled, Path, Token}）。与恢复的 B1 核心
// （New(opts...) *Registry 仪器面）的关系：Registry 保持原签名不动，挂载面以
// NewMount 提供，内部复用同一 Registry——共享时组件仪表（outboxx backlog、
// workers 存活计数等）与 HTTP 请求指标汇入同一个 /metrics 端点。

// Config 配置 /metrics 端点挂载与 HTTP 请求中间件。
type Config struct {
	// Enabled 用指针表达"默认启用"：nil 或 *true = 启用；*false = 关闭，
	// NewMount 返回 no-op 实现（Handler 一律 404、Middleware 直通、不装配
	// 任何 OTel/prometheus 设施）——适配"env 门控静默降级"部署模式。
	Enabled *bool
	// Path 是 /metrics 端点的挂载路径，空 = "/metrics"。
	Path string
	// Token 是可选端点门禁（orkome ADR 0003）：空 = 不设防；非空时要求
	// Authorization: Bearer <token> 或 ?token=<token> 匹配（常数时间比较）。
	// 校验失败返回 404 而非 401——比验收原文更强一档：不向未授权方确认
	// 端点存在（保留恢复实现的既有语义，见 Registry.HandlerWithToken）。
	Token string
}

// defaultPath 是 Path 的缺省值。
const defaultPath = "/metrics"

// WithRegistry 让 NewMount 复用既有 Registry 而非内建一个新的
// （组件仪表钩子与请求中间件汇入同一注册表/端点时用）。
func WithRegistry(r *Registry) Option {
	return func(c *config) { c.registry = r }
}

// Mount 是一次可挂载的指标装配：端点处理器 + 请求中间件 + 底层 Registry。
// 零值不可用，须通过 NewMount 创建；Enabled=false 时为 no-op 形态。
type Mount struct {
	reg     *Registry // nil = 关闭态 no-op
	path    string
	handler http.Handler // Token 门已在构造期包好（未设 Token = 裸处理器）

	// Middleware 的 route 标签基数上限记账（懒初始化，见 routeLabel）。
	seenMu sync.Mutex
	seen   map[string]struct{}
}

// NewMount 按 cfg 装配挂载面。Enabled=false 时零开销降级（不创建 Registry）；
// 未传 WithRegistry 时内建私有 Registry，并为请求时延直方图配上秒级默认桶
// （opts 已显式定制同名桶时尊重定制）。
func NewMount(cfg Config, opts ...Option) *Mount {
	var c config
	for _, o := range opts {
		o(&c)
	}
	if cfg.Path == "" {
		cfg.Path = defaultPath
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return &Mount{path: cfg.Path}
	}

	reg := c.registry
	if reg == nil {
		hasView := false
		for _, hv := range c.histogramViews {
			if hv.name == MetricHTTPDurationSeconds {
				hasView = true
				break
			}
		}
		if !hasView {
			opts = append(opts, WithHistogramBuckets(MetricHTTPDurationSeconds, DefaultDurationBuckets))
		}
		reg = New(opts...)
	}

	h := reg.Handler()
	if cfg.Token != "" {
		h = reg.HandlerWithToken(cfg.Token)
	}
	return &Mount{reg: reg, path: cfg.Path, handler: h}
}

// Path 返回端点挂载路径（mux.Handle(m.Path(), m.Handler())）。
func (m *Mount) Path() string { return m.path }

// Handler 返回 /metrics 端点处理器（含 Token 门，见 Config.Token）。
// 关闭态返回 404 处理器——调用方无需按开关分支，挂载无害。
func (m *Mount) Handler() http.Handler {
	if m.reg == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

// Registry 返回底层 Registry（组件仪表钩子注入用）；关闭态为 nil。
func (m *Mount) Registry() *Registry { return m.reg }
