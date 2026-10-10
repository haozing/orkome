// Package metrics 提供进程指标 facade：以 OTel MeterProvider 为采集面，
// 经 prometheus exporter 桥接到私有 prometheus.Registry，再由 Handler
// 返回的 promhttp 处理器在 /metrics 端点吐出。
//
// 结构约定：
//   - 注册表是私有的，不触碰 prometheus 全局 DefaultRegisterer，多个
//     Registry 实例完全隔离（测试并行、同进程多套装配都依赖这一点）；
//     创建时自动挂载 go/process 运行时采集器。
//   - 计数与量规走双轨：OTel 仪器负责标准导出，原子镜像（atomic.Int64）
//     负责进程内同步可读（Snapshot 不依赖采集周期）。直方图只有 OTel
//     轨——观测值序列无法收敛为单个整数，镜像没有意义。
//   - 指标名由调用方任意给定，首次使用时懒创建仪器并缓存；命名非法等
//     创建失败降级为 no-op（仅镜像轨继续工作），观测路径永不影响业务。
//   - Registry 的方法集满足 eventbus / outboxx / wsx 等组件声明的
//     Metrics 接口，可直接作为实现注入。
package metrics

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Registry 承载一套独立的完整指标装配：prometheus 注册表、OTel
// provider、懒创建的仪器缓存与原子镜像表。零值不可用，须通过 New 创建。
type Registry struct {
	provider *sdkmetric.MeterProvider
	promReg  *prometheus.Registry
	meter    metric.Meter

	mu       sync.Mutex
	counters map[string]metric.Int64Counter
	histos   map[string]metric.Float64Histogram
	gauges   map[string]metric.Float64Gauge
	mirrors  map[string]*atomic.Int64
}

// Option 配置 New 的可选行为。
type Option func(*config)

type config struct {
	histogramViews []histogramView
	registry       *Registry // WithRegistry：NewMount 复用既有 Registry
}

// histogramView 描述某个直方图指标的定制累积桶边界。
type histogramView struct {
	name       string
	boundaries []float64
}

// WithHistogramBuckets 为名为 name 的直方图指标定制累积桶边界。
// OTel 默认桶以毫秒量级为主，对"秒级等待/耗时"类指标分辨力不足；
// 时延类指标应显式给出贴合业务量级的边界，桶内样本计数才有意义。
// 可重复调用，为多个指标分别定制。
func WithHistogramBuckets(name string, buckets []float64) Option {
	return func(c *config) {
		c.histogramViews = append(c.histogramViews, histogramView{name: name, boundaries: buckets})
	}
}

// New 构建独立 Registry，并挂载 go/process 运行时采集器。
func New(opts ...Option) *Registry {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	promReg := prometheus.NewRegistry()
	promReg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// 在全新私有注册表上桥接 exporter 实际不可能失败（失败仅发生在
	// 同名 collector 重复注册），视为装配期 bug，panic 快速暴露。
	exp, err := otelprom.New(otelprom.WithRegisterer(promReg), otelprom.WithoutScopeInfo())
	if err != nil {
		panic("metrics: prometheus exporter: " + err.Error())
	}

	views := make([]sdkmetric.View, 0, len(cfg.histogramViews))
	for _, hv := range cfg.histogramViews {
		views = append(views, sdkmetric.NewView(
			sdkmetric.Instrument{Name: hv.name},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: hv.boundaries, NoMinMax: false,
			}},
		))
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exp),
		sdkmetric.WithView(views...),
	)
	return &Registry{
		provider: provider,
		promReg:  promReg,
		meter:    provider.Meter("ploykit/metrics"),
		counters: map[string]metric.Int64Counter{},
		histos:   map[string]metric.Float64Histogram{},
		gauges:   map[string]metric.Float64Gauge{},
		mirrors:  map[string]*atomic.Int64{},
	}
}

// InstallGlobal 把本 Registry 的 provider 安装为 OTel 全局 MeterProvider，
// 并同时安装 W3C TraceContext 传播器——链路头解析与指标导出共用这套
// 进程级约定。仅在进程装配期调用一次。
func (r *Registry) InstallGlobal() {
	otel.SetMeterProvider(r.provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

// mirror 返回（或首次创建）key 对应的原子镜像。镜像只增不删：指标集
// 是进程生命周期内单调增长的有限集合，无需清理。
func (r *Registry) mirror(key string) *atomic.Int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.mirrors[key]; ok {
		return m
	}
	m := &atomic.Int64{}
	r.mirrors[key] = m
	return m
}

// counter 返回（或首次创建并缓存）name 的计数仪器；创建失败缓存 nil，
// 让后续调用稳定走 no-op 路径，坏名字不 panic 也不刷日志。
func (r *Registry) counter(name string) metric.Int64Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c, err := r.meter.Int64Counter(name)
	if err != nil {
		r.counters[name] = nil
		return nil
	}
	r.counters[name] = c
	return c
}

func (r *Registry) histogram(name string) metric.Float64Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histos[name]; ok {
		return h
	}
	h, err := r.meter.Float64Histogram(name)
	if err != nil {
		r.histos[name] = nil
		return nil
	}
	r.histos[name] = h
	return h
}

func (r *Registry) gauge(name string) metric.Float64Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g, err := r.meter.Float64Gauge(name)
	if err != nil {
		r.gauges[name] = nil
		return nil
	}
	r.gauges[name] = g
	return g
}

// attrSet 把字符串属性表转为 OTel attribute.Set（空表返回空集，
// 避免无标签调用也付出 set 构造成本）。
func attrSet(attrs map[string]string) attribute.Set {
	if len(attrs) == 0 {
		return attribute.NewSet()
	}
	pairs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		pairs = append(pairs, attribute.String(k, v))
	}
	return attribute.NewSet(pairs...)
}

// snapKey 生成镜像轨的快照键：属性键排序后拼成 name{k=v,...}。
// 排序保证同一属性集合无论构造顺序如何都折叠到同一键，Snapshot
// 的结果对 map 迭代顺序稳定。
func snapKey(name string, attrs map[string]string) string {
	if len(attrs) == 0 {
		return name
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := name + "{"
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k + "=" + attrs[k]
	}
	return out + "}"
}

// Counter 返回 name 的原子镜像句柄，供不方便逐次经过 Registry 的旧式
// 调用方直接 Add。写入只进镜像轨，不进 OTel 轨。
func (r *Registry) Counter(name string) *atomic.Int64 { return r.mirror(name) }

// Incr 计数 +1。
func (r *Registry) Incr(name string) { r.Add(name, 1) }

// Add 计数累加 delta，镜像轨与 OTel 轨同步写入。
func (r *Registry) Add(name string, delta int64) {
	r.mirror(name).Add(delta)
	if c := r.counter(name); c != nil {
		c.Add(context.Background(), delta)
	}
}

// IncrAttrs 带标签计数 +1。镜像轨用快照键（snapKey）区分标签组合，
// OTel 轨用 attribute set 承载同一信息。
func (r *Registry) IncrAttrs(name string, attrs map[string]string) {
	r.mirror(snapKey(name, attrs)).Add(1)
	if c := r.counter(name); c != nil {
		c.Add(context.Background(), 1, metric.WithAttributeSet(attrSet(attrs)))
	}
}

// Observe 记录直方图观测值（仅 OTel 轨，无镜像）。
func (r *Registry) Observe(name string, v float64) {
	if h := r.histogram(name); h != nil {
		h.Record(context.Background(), v)
	}
}

// ObserveAttrs 记录带标签的直方图观测值（仅 OTel 轨；观测值序列无法收敛为
// 单个整数，与 Observe 一样不设镜像）。中间件的时延直方图走这一入口。
func (r *Registry) ObserveAttrs(name string, v float64, attrs map[string]string) {
	if h := r.histogram(name); h != nil {
		h.Record(context.Background(), v, metric.WithAttributeSet(attrSet(attrs)))
	}
}

// Gauge 设置量规当前值。镜像轨截断小数部分存 int64（量规语义下取整
// 的分辨率损失可接受），OTel 轨保留原值。
func (r *Registry) Gauge(name string, v float64) {
	r.mirror(name).Store(int64(v))
	if g := r.gauge(name); g != nil {
		g.Record(context.Background(), v)
	}
}

// Snapshot 返回全部镜像的当前值，键含标签展开形态（name{k=v,...}）。
// 只反映镜像轨；仅 OTel 轨的直方图观测不出现在这里。
func (r *Registry) Snapshot() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.mirrors))
	for k, m := range r.mirrors {
		out[k] = m.Load()
	}
	return out
}
