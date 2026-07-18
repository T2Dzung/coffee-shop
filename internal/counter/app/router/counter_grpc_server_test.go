package router

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	gen "github.com/thangchung/go-coffeeshop/proto/gen"
	"go.opentelemetry.io/otel/trace"
)

func TestPlaceOrderInvalidLoyaltyMemberIDLogsTraceAwareError(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	server := &counterGRPCServer{}
	_, err := server.PlaceOrder(ctx, &gen.PlaceOrderRequest{LoyaltyMemberId: "sensitive-invalid-value"})
	if err == nil {
		t.Fatal("PlaceOrder() error = nil, want invalid UUID error")
	}

	logLine := output.String()
	for _, expected := range []string{
		`"level":"ERROR"`,
		`"msg":"invalid place order request"`,
		`"rpc_method":"PlaceOrder"`,
		`"trace_id":"01000000000000000000000000000000"`,
		`"span_id":"0200000000000000"`,
	} {
		if !strings.Contains(logLine, expected) {
			t.Errorf("log output %q does not contain %q", logLine, expected)
		}
	}
	if strings.Contains(logLine, "sensitive-invalid-value") {
		t.Errorf("log output leaked invalid loyalty member ID: %q", logLine)
	}
}
