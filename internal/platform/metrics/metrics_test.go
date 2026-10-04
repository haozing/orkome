package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"
)

func TestFacadeCounterAndSnapshot(t *testing.T) {
	r := New()
	r.Incr("task_completed")
	r.Incr("task_completed")
	r.Add("outbox_replayed", 3)
	assert.Equal(t, int64(2), r.Snapshot()["task_completed"])
	assert.Equal(t, int64(3), r.Snapshot()["outbox_replayed"])

	r.Counter("legacy_handle").Add(5)
	assert.Equal(t, int64(5), r.Snapshot()["legacy_handle"])
}

func TestFacadeAttrsCounter(t *testing.T) {
	r := New()
	r.IncrAttrs("sweep_hits", map[string]string{"kind": "runtime_recovery"})
	r.IncrAttrs("sweep_hits", map[string]string{"kind": "runtime_recovery"})
	r.IncrAttrs("sweep_hits", map[string]string{"kind": "interrupt_timeout"})
	snap := r.Snapshot()
	assert.Equal(t, int64(2), snap[`sweep_hits{kind=runtime_recovery}`])
	assert.Equal(t, int64(1), snap[`sweep_hits{kind=interrupt_timeout}`])
}

func TestPromHTTPExposition(t *testing.T) {
	r := New()
	r.Incr("task_completed")
	r.IncrAttrs("sweep_hits", map[string]string{"kind": "runtime_recovery"})
	r.Gauge("ws_connections", 3)
	r.Observe("task_claim_wait_seconds", 0.42)

	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "task_completed_total 1")
	assert.Contains(t, body, `sweep_hits_total{kind="runtime_recovery"`)
	assert.Contains(t, body, "ws_connections 3")
	assert.Contains(t, body, "task_claim_wait_seconds_bucket{le=\"0.5\"} 1")
	assert.Contains(t, body, "go_goroutines")
}

func TestHandlerWithToken(t *testing.T) {
	r := New()
	r.Incr("task_completed")
	h := r.HandlerWithToken("s3cret")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics?token=s3cret", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "task_completed_total 1")

	rec = httptest.NewRecorder()
	r.HandlerWithToken("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMultipleRegistriesIndependent(t *testing.T) {
	a, b := New(), New()
	a.Incr("task_completed")
	b.Incr("task_failed")
	assert.Equal(t, int64(0), b.Snapshot()["task_completed"])
	assert.Equal(t, int64(1), a.Snapshot()["task_completed"])
}

func TestInstallGlobalSetsTraceContextPropagator(t *testing.T) {
	r := New()
	r.InstallGlobal()
	t.Cleanup(func() {

		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
		otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	})

	ctx := propagation.TraceContext{}.Extract(t.Context(),
		propagation.HeaderCarrier(http.Header{"Traceparent": {
			"00-0af7651916cd43dd8448eb211cdef345-00f067aa0ba902b7-01"}}))
	sc := trace.SpanContextFromContext(ctx)
	assert.True(t, sc.IsValid(), "after InstallGlobal the global propagator should be TraceContext")
	assert.Equal(t, "0af7651916cd43dd8448eb211cdef345", sc.TraceID().String())
}
