package telemetry

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultEndpoint    = "otel-collector.observability.svc.cluster.local:4317"
	defaultSampleRatio = 1.0
	defaultSampler     = "parentbased_traceidratio"
)

type Config struct {
	Endpoint    string
	Service     string
	Environment string
	Version     string
	Sampler     string
	SampleRatio float64
}

func ParseConfig(service, environment, version string) (Config, error) {
	if configuredService := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); configuredService != "" {
		service = configuredService
	}
	if strings.TrimSpace(service) == "" {
		return Config{}, fmt.Errorf("service name must not be empty")
	}
	if strings.TrimSpace(environment) == "" {
		return Config{}, fmt.Errorf("deployment environment must not be empty")
	}

	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if strings.Contains(endpoint, "://") {
		return Config{}, fmt.Errorf("OTLP/gRPC endpoint must be host:port, got %q", endpoint)
	}

	sampler := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER"))
	if sampler == "" {
		sampler = defaultSampler
	}
	if sampler != defaultSampler {
		return Config{}, fmt.Errorf("unsupported trace sampler %q; expected %q", sampler, defaultSampler)
	}

	ratioStr := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
	ratio := defaultSampleRatio
	if ratioStr != "" {
		parsedRatio, err := strconv.ParseFloat(ratioStr, 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid sampling ratio %q: %w", ratioStr, err)
		}
		if parsedRatio < 0.0 || parsedRatio > 1.0 {
			return Config{}, fmt.Errorf("sampling ratio %f out of bounds [0.0, 1.0]", parsedRatio)
		}
		ratio = parsedRatio
	}

	return Config{
		Endpoint:    endpoint,
		Service:     service,
		Environment: environment,
		Version:     version,
		Sampler:     sampler,
		SampleRatio: ratio,
	}, nil
}
