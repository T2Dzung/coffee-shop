package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

func WithTrace(ctx context.Context, log *slog.Logger) *slog.Logger {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return log
	}
	return log.With("trace_id", spanContext.TraceID().String(), "span_id", spanContext.SpanID().String())
}

func InfoContext(ctx context.Context, msg string, args ...any) {
	WithTrace(ctx, slog.Default()).InfoContext(ctx, msg, args...)
}
