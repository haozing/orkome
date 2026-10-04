package metrics

import (
	"context"
	"crypto/subtle"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprop "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var claimWaitBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

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

func New() *Registry {
	promReg := prometheus.NewRegistry()

	promReg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	exp, err := otelprom.New(otelprom.WithRegisterer(promReg), otelprom.WithoutScopeInfo())
	if err != nil {

		promReg = prometheus.NewRegistry()
		exp, err = otelprom.New(otelprom.WithRegisterer(promReg), otelprom.WithoutScopeInfo())
		if err != nil {
			panic("metrics: prometheus exporter: " + err.Error())
		}
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exp),

		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "task_claim_wait_seconds"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: claimWaitBuckets, NoMinMax: false,
			}},
		)),
	)
	return &Registry{
		provider: provider,
		promReg:  promReg,
		meter:    provider.Meter("orkome/metrics"),
		counters: map[string]metric.Int64Counter{},
		histos:   map[string]metric.Float64Histogram{},
		gauges:   map[string]metric.Float64Gauge{},
		mirrors:  map[string]*atomic.Int64{},
	}
}

func (r *Registry) InstallGlobal() {
	otelprop.SetMeterProvider(r.provider)
	otelprop.SetTextMapPropagator(propagation.TraceContext{})
}

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

func (r *Registry) Counter(name string) *atomic.Int64 { return r.mirror(name) }

func (r *Registry) Incr(name string) { r.Add(name, 1) }

func (r *Registry) Add(name string, delta int64) {
	r.mirror(name).Add(delta)
	if c := r.counter(name); c != nil {
		c.Add(context.Background(), delta)
	}
}

func (r *Registry) IncrAttrs(name string, attrs map[string]string) {
	r.mirror(snapKey(name, attrs)).Add(1)
	if c := r.counter(name); c != nil {
		c.Add(context.Background(), 1, metric.WithAttributeSet(attrSet(attrs)))
	}
}

func (r *Registry) Observe(name string, v float64) {
	if h := r.histogram(name); h != nil {
		h.Record(context.Background(), v)
	}
}

func (r *Registry) Gauge(name string, v float64) {
	r.mirror(name).Store(int64(v))
	if g := r.gauge(name); g != nil {
		g.Record(context.Background(), v)
	}
}

func (r *Registry) Snapshot() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.mirrors))
	for k, m := range r.mirrors {
		out[k] = m.Load()
	}
	return out
}

func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{})
}

func (r *Registry) HandlerWithToken(token string) http.Handler {
	h := r.Handler()
	if token == "" {
		return h
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ok := subtle.ConstantTimeCompare([]byte(bearerToken(req.Header.Get("Authorization"))), want) == 1
		if !ok {
			ok = subtle.ConstantTimeCompare([]byte(req.URL.Query().Get("token")), want) == 1
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		h.ServeHTTP(w, req)
	})
}

func bearerToken(auth string) string {
	const prefix = "Bearer "
	if len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):]
	}
	return ""
}
