package telemetry

import "testing"

func TestParseConfigDefaults(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("OTEL_TRACES_SAMPLER", "")
	t.Setenv("OTEL_SERVICE_NAME", "")

	cfg, err := ParseConfig("test-service", "dev", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Endpoint != defaultEndpoint {
		t.Errorf("got endpoint %q, want %q", cfg.Endpoint, defaultEndpoint)
	}
	if cfg.SampleRatio != defaultSampleRatio {
		t.Errorf("got ratio %f, want %f", cfg.SampleRatio, defaultSampleRatio)
	}
	if cfg.Sampler != defaultSampler {
		t.Errorf("got sampler %q, want %q", cfg.Sampler, defaultSampler)
	}
	if cfg.Service != "test-service" || cfg.Environment != "dev" || cfg.Version != "1.0.0" {
		t.Errorf("missing core fields: %+v", cfg)
	}
}

func TestParseConfigExplicitValues(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
	t.Setenv("OTEL_TRACES_SAMPLER", defaultSampler)
	t.Setenv("OTEL_SERVICE_NAME", "override-service")

	cfg, err := ParseConfig("test-service", "dev", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Endpoint != "localhost:4317" {
		t.Errorf("got endpoint %q, want %q", cfg.Endpoint, "localhost:4317")
	}
	if cfg.SampleRatio != 0.5 {
		t.Errorf("got ratio %f, want %f", cfg.SampleRatio, 0.5)
	}
	if cfg.Service != "override-service" {
		t.Errorf("got service %q, want environment override", cfg.Service)
	}
}

func TestParseConfigInvalidRatio(t *testing.T) {
	tests := []string{"invalid", "-0.1", "1.1"}
	for _, input := range tests {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", input)
		_, err := ParseConfig("test", "dev", "1")
		if err == nil {
			t.Errorf("expected error for ratio %q, got nil", input)
		}
	}
}

func TestParseConfigRejectsUnsupportedTransportAndSampler(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317")
	if _, err := ParseConfig("test", "dev", "1"); err == nil {
		t.Fatal("expected endpoint validation error")
	}

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4317")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	if _, err := ParseConfig("test", "dev", "1"); err == nil {
		t.Fatal("expected sampler validation error")
	}
}
