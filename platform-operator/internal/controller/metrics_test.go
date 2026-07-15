package controller

import (
	"errors"
	"slices"
	"testing"

	dto "github.com/prometheus/client_model/go"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestWriteMetricUsesOnlyBoundedLabels(t *testing.T) {
	recordWrite(writeOperationApply, writeResourceDeployment, nil)
	recordWrite(writeOperationApplyDryRun, writeResourceService, nil)
	recordWrite(writeOperationDelete, writeResourceService, nil)
	recordWrite(writeOperationStatusPatch, writeResourceCoffeeShopSvc, errors.New("fixture"))

	families, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	var family *dto.MetricFamily
	for _, candidate := range families {
		if candidate.GetName() == writeOperationsMetricName {
			family = candidate
			break
		}
	}
	if family == nil {
		t.Fatalf("metric family %q was not registered", writeOperationsMetricName)
	}

	allowedLabelNames := []string{"operation", "resource", "result"}
	allowedOperations := []string{
		string(writeOperationApply),
		string(writeOperationApplyDryRun),
		string(writeOperationDelete),
		string(writeOperationStatusPatch),
	}
	allowedResources := []string{
		string(writeResourceDeployment),
		string(writeResourceService),
		string(writeResourceCoffeeShopSvc),
	}

	for _, metric := range family.Metric {
		if len(metric.Label) != len(allowedLabelNames) {
			t.Fatalf("metric has %d labels, want exactly %d", len(metric.Label), len(allowedLabelNames))
		}
		for _, label := range metric.Label {
			if !slices.Contains(allowedLabelNames, label.GetName()) {
				t.Fatalf("unbounded or unexpected label %q", label.GetName())
			}
			switch label.GetName() {
			case "operation":
				if !slices.Contains(allowedOperations, label.GetValue()) {
					t.Fatalf("unexpected operation label value %q", label.GetValue())
				}
			case "resource":
				if !slices.Contains(allowedResources, label.GetValue()) {
					t.Fatalf("unexpected resource label value %q", label.GetValue())
				}
			case "result":
				if label.GetValue() != writeResultSuccess && label.GetValue() != writeResultError {
					t.Fatalf("unexpected result label value %q", label.GetValue())
				}
			}
		}
	}
}
