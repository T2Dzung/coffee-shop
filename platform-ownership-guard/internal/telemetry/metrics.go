package telemetry

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

const (
	MetricSubsystem = "platform_ownership_guard"

	ResultSuccess          = "success"
	ResultTerminalError    = "terminal_error"
	ResultTransientError   = "transient_error"
	ResultStatusWriteError = "status_write_error"
	ResultAuditReadError   = "audit_read_error"

	SourceCollector   = "collector"
	SourceDiscovery   = "discovery"
	SourceApplication = "application"
	SourceTarget      = "target"
	SourceOwner       = "owner"
)

// Metrics holds the registered Prometheus metrics for PlatformOwnershipGuard.
type Metrics struct {
	ScansTotal              *prometheus.CounterVec
	ScanDurationSeconds     *prometheus.HistogramVec
	InventoryErrorsTotal    *prometheus.CounterVec
	FindingTransitionsTotal *prometheus.CounterVec
}

// NewMetrics initializes and registers bounded metrics with the provided Registerer.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		ScansTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: MetricSubsystem,
				Name:      "scans_total",
				Help:      "Total number of audit scan attempts handled by the controller, partitioned by result.",
			},
			[]string{"result"},
		),
		ScanDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Subsystem: MetricSubsystem,
				Name:      "scan_duration_seconds",
				Help:      "Duration of audit scan attempts in seconds, partitioned by result.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"result"},
		),
		InventoryErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: MetricSubsystem,
				Name:      "inventory_errors_total",
				Help:      "Total number of distinct inventory collection errors, partitioned by source and reason class.",
			},
			[]string{"source", "reason"},
		),
		FindingTransitionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: MetricSubsystem,
				Name:      "finding_transitions_total",
				Help:      "Total number of finding lifecycle transitions published by the controller.",
			},
			[]string{"detector", "severity", "confidence", "transition"},
		),
	}

	collectors := []prometheus.Collector{
		m.ScansTotal,
		m.ScanDurationSeconds,
		m.InventoryErrorsTotal,
		m.FindingTransitionsTotal,
	}

	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("failed to register telemetry metric: %w", err)
		}
	}

	return m, nil
}

// RecordScan records scan outcome and duration in seconds.
func (m *Metrics) RecordScan(result string, duration time.Duration) {
	if m == nil {
		return
	}
	m.ScansTotal.WithLabelValues(result).Inc()
	m.ScanDurationSeconds.WithLabelValues(result).Observe(duration.Seconds())
}

// RecordInventoryError records a single distinct inventory error by source and reason.
func (m *Metrics) RecordInventoryError(source, reason string) {
	if m == nil || source == "" || reason == "" {
		return
	}
	m.InventoryErrorsTotal.WithLabelValues(source, reason).Inc()
}

// RecordTransition records a finding lifecycle transition event.
func (m *Metrics) RecordTransition(detector guardplatformv1alpha1.DetectorType, severity guardplatformv1alpha1.FindingSeverity, confidence guardplatformv1alpha1.FindingConfidence, transition string) {
	if m == nil {
		return
	}
	m.FindingTransitionsTotal.WithLabelValues(
		string(detector),
		string(severity),
		string(confidence),
		transition,
	).Inc()
}
