package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestNewWritesJSONWithStableAttributesAndGroups(t *testing.T) {
	var output bytes.Buffer
	log := New(Config{Service: "counter", Environment: "test", Version: "1.2.3", Level: "debug", Writer: &output})
	log.WithGroup("request").Info("handled", "method", "POST")
	record := decodeRecord(t, output.String())
	if record["msg"] != "handled" || record["level"] != "INFO" {
		t.Fatalf("unexpected record: %#v", record)
	}
	if record["service"] != "counter" || record["environment"] != "test" || record["version"] != "1.2.3" {
		t.Fatalf("missing stable attributes: %#v", record)
	}
	request, ok := record["request"].(map[string]any)
	if !ok || request["method"] != "POST" {
		t.Fatalf("group attributes were not preserved: %#v", record)
	}
}

func TestNewFiltersLevels(t *testing.T) {
	var output bytes.Buffer
	log := New(Config{Service: "product", Environment: "test", Level: "warn", Writer: &output})
	log.Info("filtered")
	log.Warn("visible")
	if strings.Contains(output.String(), "filtered") || !strings.Contains(output.String(), "visible") {
		t.Fatalf("unexpected level filtering: %q", output.String())
	}
}

func TestSetDefaultRoutesTopLevelCalls(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)
	var output bytes.Buffer
	SetDefault(Config{Service: "proxy", Environment: "test", Writer: &output})
	slog.Info("top-level")
	record := decodeRecord(t, output.String())
	if record["msg"] != "top-level" || record["service"] != "proxy" {
		t.Fatalf("top-level slog call did not use configured logger: %#v", record)
	}
}

func TestParseLevelUsesSafeInfoFallback(t *testing.T) {
	tests := map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "error": slog.LevelError, "info": slog.LevelInfo, "typo": slog.LevelInfo, "": slog.LevelInfo}
	for input, want := range tests {
		if got := ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestLoggerRedactsSecretAttributes(t *testing.T) {
	const secret = "postgres://user:secret-canary@database/app"
	var output bytes.Buffer
	log := New(Config{Service: "counter", Environment: "test", Writer: &output})
	log.Error("database connection failed", "error", "connection refused", "dsn", secret)
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "postgres://") || !strings.Contains(output.String(), redactedValue) {
		t.Fatalf("secret leaked into log: %q", output.String())
	}
}

func TestWithTraceAddsIDsOnlyForValidSpanContext(t *testing.T) {
	var output bytes.Buffer
	log := New(Config{Service: "proxy", Environment: "test", Writer: &output})
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	WithTrace(ctx, log).InfoContext(ctx, "with trace")
	record := decodeRecord(t, output.String())
	if record["trace_id"] != traceID.String() || record["span_id"] != spanID.String() {
		t.Fatalf("missing trace correlation fields: %#v", record)
	}
	output.Reset()
	WithTrace(context.Background(), log).Info("without trace")
	record = decodeRecord(t, output.String())
	if _, exists := record["trace_id"]; exists {
		t.Fatalf("background context must not emit trace_id: %#v", record)
	}
}

func decodeRecord(t *testing.T, line string) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not valid JSON: %v; line=%q", err, line)
	}
	return record
}
