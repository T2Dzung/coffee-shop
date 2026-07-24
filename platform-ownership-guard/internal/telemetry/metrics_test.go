package telemetry_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/telemetry"
)

func TestMetricsRegistrationAndBoundedLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := telemetry.NewMetrics(reg)
	if err != nil {
		t.Fatalf("expected NewMetrics to succeed, got %v", err)
	}

	// 1. Duplicate registration returns error instead of panic
	_, errDup := telemetry.NewMetrics(reg)
	if errDup == nil {
		t.Fatalf("expected error on duplicate metrics registration, got nil")
	}

	// 2. Record values
	m.RecordScan(telemetry.ResultSuccess, 150*time.Millisecond)
	m.RecordInventoryError(telemetry.SourceCollector, "TransientReadFailure")
	m.RecordTransition(guardplatformv1alpha1.DetectorArgoPruneRisk, guardplatformv1alpha1.SeverityHigh, guardplatformv1alpha1.ConfidenceConfirmed, "added")

	// 3. Gather metrics and inspect label names (ensure no high-cardinality identity labels)
	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather registered metrics: %v", err)
	}

	disallowedLabels := map[string]bool{
		"namespace":  true,
		"name":       true,
		"uid":        true,
		"finding_id": true,
		"audit_name": true,
	}

	for _, mf := range metricFamilies {
		for _, metric := range mf.Metric {
			for _, label := range metric.Label {
				if disallowedLabels[label.GetName()] {
					t.Errorf("disallowed high-cardinality label '%s' found in metric '%s'", label.GetName(), mf.GetName())
				}
			}
		}
	}
}

func TestMetricsValueGathering(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := telemetry.NewMetrics(reg)
	if err != nil {
		t.Fatalf("failed to create metrics: %v", err)
	}

	m.RecordScan(telemetry.ResultSuccess, 200*time.Millisecond)
	m.RecordTransition(guardplatformv1alpha1.DetectorStaleOwnerReference, guardplatformv1alpha1.SeverityWarning, guardplatformv1alpha1.ConfidenceConfirmed, "resolved")

	metricFamilies, _ := reg.Gather()
	var foundScanCounter bool
	var foundTransitionCounter bool

	for _, mf := range metricFamilies {
		if mf.GetName() == "platform_ownership_guard_scans_total" {
			foundScanCounter = true
			if getCounterVal(mf.Metric[0]) != 1 {
				t.Errorf("expected scans_total count 1, got %f", getCounterVal(mf.Metric[0]))
			}
		}
		if mf.GetName() == "platform_ownership_guard_finding_transitions_total" {
			foundTransitionCounter = true
			if getCounterVal(mf.Metric[0]) != 1 {
				t.Errorf("expected finding_transitions_total count 1, got %f", getCounterVal(mf.Metric[0]))
			}
		}
	}

	if !foundScanCounter || !foundTransitionCounter {
		t.Errorf("expected scan counter and transition counter in gathered metrics")
	}
}

func TestRecordSnapshotInventoryErrorsDeduplicatesAndIncludesStaleApplication(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := telemetry.NewMetrics(reg)
	if err != nil {
		t.Fatalf("failed to create metrics: %v", err)
	}

	forbidden := &inventory.ErrorDTO{Class: inventory.ErrEvidenceForbidden}
	snapshot := &inventory.NormalizedSnapshot{
		Applications: []inventory.ApplicationEvidence{
			{Metadata: inventory.ObservationMetadata{Freshness: inventory.FreshnessStale}},
			{Metadata: inventory.ObservationMetadata{Freshness: inventory.FreshnessStale}},
		},
		Owners: []inventory.OwnerEvidence{
			{Metadata: inventory.ObservationMetadata{SourceError: forbidden}},
			{Metadata: inventory.ObservationMetadata{SourceError: forbidden}},
		},
	}

	telemetry.RecordSnapshotInventoryErrors(m, snapshot, nil)
	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	values := map[string]float64{}
	for _, family := range metricFamilies {
		if family.GetName() != "platform_ownership_guard_inventory_errors_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			values[labels["source"]+"|"+labels["reason"]] = getCounterVal(metric)
		}
	}

	if values[telemetry.SourceApplication+"|"+string(inventory.ErrStaleEvidence)] != 1 {
		t.Fatalf("expected one deduplicated stale application error, got %#v", values)
	}
	if values[telemetry.SourceOwner+"|"+string(inventory.ErrEvidenceForbidden)] != 1 {
		t.Fatalf("expected one deduplicated forbidden owner error, got %#v", values)
	}
}

func getCounterVal(m *dto.Metric) float64 {
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return 0
}
