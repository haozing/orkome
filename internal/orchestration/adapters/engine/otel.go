package engine

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func engineMeter() metric.Meter {
	return otel.GetMeterProvider().Meter("orkome/engine")
}

func observeAbsorbed(retries, failovers int) {
	if retries > 0 {
		if c, err := engineMeter().Int64Counter("engine_model_retry_absorbed"); err == nil {
			c.Add(context.Background(), int64(retries), metric.WithAttributes(attribute.String("kind", "retry")))
		}
	}
	if failovers > 0 {
		if c, err := engineMeter().Int64Counter("engine_model_retry_absorbed"); err == nil {
			c.Add(context.Background(), int64(failovers), metric.WithAttributes(attribute.String("kind", "failover")))
		}
	}
}

func observeGenerate(kind string) {
	if c, err := engineMeter().Int64Counter("engine_generate_calls"); err == nil {
		c.Add(context.Background(), 1, metric.WithAttributes(attribute.String("kind", kind)))
	}
}

func startEngineSpan(ctx context.Context, taskID, workspaceID string) (context.Context, trace.Span) {
	return otel.GetTracerProvider().Tracer("orkome/engine").Start(ctx, "engine.Execute",
		trace.WithAttributes(
			attribute.String("task.id", taskID),
			attribute.String("workspace.id", workspaceID),
		))
}
