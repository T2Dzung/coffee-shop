package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

type recordingTelemetry struct {
	transitions []string
}

func (*recordingTelemetry) RecordScan(string, time.Duration)    {}
func (*recordingTelemetry) RecordInventoryError(string, string) {}
func (*recordingTelemetry) RecordSnapshotErrors(*inventory.NormalizedSnapshot, error) {
}
func (r *recordingTelemetry) RecordTransition(_ guardplatformv1alpha1.DetectorType, _ guardplatformv1alpha1.FindingSeverity, _ guardplatformv1alpha1.FindingConfidence, transition string) {
	r.transitions = append(r.transitions, transition)
}

func TestPublishEventsAndMetricsUsesTransitionContract(t *testing.T) {
	audit := &guardplatformv1alpha1.OwnershipAudit{ObjectMeta: metav1.ObjectMeta{Name: "audit", Namespace: "default"}}
	finding := guardplatformv1alpha1.OwnershipFinding{
		ID:         "stable-id",
		Detector:   guardplatformv1alpha1.DetectorArgoPruneRisk,
		Severity:   guardplatformv1alpha1.SeverityHigh,
		Confidence: guardplatformv1alpha1.ConfidenceConfirmed,
		Target: guardplatformv1alpha1.ResourceReference{
			APIGroup: "apps", Version: "v1", Kind: "ReplicaSet", Namespace: "coffeeshop", Name: "coffee-rs",
		},
	}

	recorder := &FakeEventRecorder{}
	metrics := &recordingTelemetry{}
	reconciler := &OwnershipAuditReconciler{Recorder: recorder, Telemetry: metrics}
	reconciler.publishEventsAndMetrics(audit, []FindingTransition{
		{Type: TransitionAdded, Finding: finding},
		{Type: TransitionChanged, Finding: finding},
		{Type: TransitionResolved, Finding: finding},
		{Type: TransitionUnchanged, Finding: finding},
	})

	events := recorder.GetEvents()
	if len(events) != 3 || len(metrics.transitions) != 3 {
		t.Fatalf("expected exactly three published transitions, events=%d metrics=%v", len(events), metrics.transitions)
	}
	wantReasons := []string{"FindingDetected", "FindingChanged", "FindingResolved"}
	wantTypes := []string{"Warning", "Warning", "Normal"}
	for i := range events {
		if events[i].Reason != wantReasons[i] || events[i].Type != wantTypes[i] || events[i].Action != "AuditFinding" {
			t.Fatalf("unexpected event %d: %#v", i, events[i])
		}
		if !strings.Contains(events[i].Note, "target=apps/v1/ReplicaSet/coffeeshop/coffee-rs") || len(events[i].Note) > 512 {
			t.Fatalf("event note must contain bounded GVK identity: %q", events[i].Note)
		}
	}
}
