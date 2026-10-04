package webx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func withTraceContext(t *testing.T) {
	t.Helper()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	t.Cleanup(func() {
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
		otel.SetMeterProvider(sdkmetric.NewMeterProvider())
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(noopSyncer{})))
	})
}

type noopSyncer struct{}

func (noopSyncer) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (noopSyncer) Shutdown(context.Context) error                             { return nil }

func TestTraceMiddleware_ContinuesUpstreamTrace(t *testing.T) {
	withTraceContext(t)
	const upstream = "00-0af7651916cd43dd8448eb211cdef345-00f067aa0ba902b7-01"

	var seen trace.SpanContext
	h := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/work", nil)
	req.Header.Set("traceparent", upstream)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, seen.IsValid(), "downstream handler ctx must carry the span")
	assert.Equal(t, "0af7651916cd43dd8448eb211cdef345", seen.TraceID().String(),
		"upstream trace must continue (same trace id)")
	assert.NotEqual(t, "00f067aa0ba902b7", seen.SpanID().String(), "server spans should have their own span id")

	respTP := rec.Header().Get("traceparent")
	require.NotEmpty(t, respTP, "the response must carry traceparent back")
	assert.Contains(t, respTP, "0af7651916cd43dd8448eb211cdef345", "response carries the same trace id")
}

func TestTraceMiddleware_RootSpan(t *testing.T) {
	withTraceContext(t)

	var seen trace.SpanContext
	h := Trace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = trace.SpanContextFromContext(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", nil))

	assert.True(t, seen.IsValid(), "should start a root span even without an upstream")
	assert.NotEmpty(t, seen.TraceID().String())
	assert.Contains(t, rec.Header().Get("traceparent"), seen.TraceID().String(),
		"root span's response carries the same trace id")
}
