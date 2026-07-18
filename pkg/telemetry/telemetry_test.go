package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type recordingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (*recordingExporter) Shutdown(context.Context) error { return nil }

func (e *recordingExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), e.spans...)
}

func TestNewTelemetryLifecycle(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=ignored-by-explicit-config")

	exp := &recordingExporter{}

	cfg := Config{
		Endpoint:    "localhost:4317",
		Service:     "test-service",
		Environment: "test-env",
		Version:     "1.0.0",
		SampleRatio: 1.0,
	}

	tp, shutdown, err := newTracerProvider(cfg, WithExporter(exp))
	if err != nil {
		t.Fatalf("failed to init telemetry: %v", err)
	}

	tracer := tp.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "test-span")
	if !span.IsRecording() {
		t.Fatal("root span is not recording with SampleRatio=1")
	}
	span.End()

	// Shutdown flushes spans
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("failed to shutdown: %v", err)
	}

	spans := exp.getSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	if s.Name() != "test-span" {
		t.Errorf("got span name %q, want %q", s.Name(), "test-span")
	}

	res := s.Resource()
	attrs := res.Attributes()
	var serviceName, envName, verName string
	for _, attr := range attrs {
		switch attr.Key {
		case attribute.Key("service.name"):
			serviceName = attr.Value.AsString()
		case attribute.Key("deployment.environment.name"):
			envName = attr.Value.AsString()
		case attribute.Key("service.version"):
			verName = attr.Value.AsString()
		}
	}

	if serviceName != "test-service" {
		t.Errorf("got service.name %q, want %q", serviceName, "test-service")
	}
	if envName != "test-env" {
		t.Errorf("got deployment.environment.name %q, want %q", envName, "test-env")
	}
	if verName != "1.0.0" {
		t.Errorf("got service.version %q, want %q", verName, "1.0.0")
	}
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("idempotent shutdown returned error: %v", err)
	}
}

func TestParentBasedSamplerRespectsSampledParent(t *testing.T) {
	exp := &recordingExporter{}
	tp, shutdown, err := newTracerProvider(Config{Service: "test", Environment: "dev", Version: "1", SampleRatio: 0}, WithExporter(exp))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, root := tp.Tracer("test").Start(context.Background(), "unsampled-root")
	root.End()

	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), parent)
	_, child := tp.Tracer("test").Start(ctx, "sampled-child")
	if !child.IsRecording() {
		t.Fatal("sampled remote parent did not produce a recording child span")
	}
	child.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
	if got := len(exp.getSpans()); got != 1 {
		t.Fatalf("exported spans = %d, want 1 sampled child", got)
	}
}

func TestNewDoesNotBlockWhenCollectorIsUnreachable(t *testing.T) {
	started := time.Now()
	shutdown, err := New(Config{
		Endpoint: "127.0.0.1:1", Service: "test", Environment: "test", Version: "1", SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("New() blocked for %s while collector was unreachable", elapsed)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = shutdown(shutdownCtx)
}
