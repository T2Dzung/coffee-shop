package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type ShutdownFunc func(context.Context) error

type options struct {
	exporter sdktrace.SpanExporter
}

type Option func(*options)

func WithExporter(exporter sdktrace.SpanExporter) Option {
	return func(o *options) {
		o.exporter = exporter
	}
}

func New(cfg Config, opts ...Option) (ShutdownFunc, error) {
	tp, shutdown, err := newTracerProvider(cfg, opts...)
	if err != nil {
		return nil, err
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return shutdown, nil
}

func newTracerProvider(cfg Config, opts ...Option) (*sdktrace.TracerProvider, ShutdownFunc, error) {
	ctx := context.Background()

	var opt options
	for _, o := range opts {
		o(&opt)
	}

	var exporter sdktrace.SpanExporter
	if opt.exporter != nil {
		exporter = opt.exporter
	} else {
		// Dùng OTLP/gRPC exporter không đồng bộ (không block dial khi start)
		var err error
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
		}
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			attribute.String("service.name", cfg.Service),
			attribute.String("service.version", cfg.Version),
			attribute.String("deployment.environment.name", cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(1*time.Second),
			sdktrace.WithExportTimeout(3*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func(shutdownCtx context.Context) error {
		shutdownOnce.Do(func() {
			shutdownErr = tp.Shutdown(shutdownCtx)
		})
		return shutdownErr
	}

	return tp, shutdown, nil
}
