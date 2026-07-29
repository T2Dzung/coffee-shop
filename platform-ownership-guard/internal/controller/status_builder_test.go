package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

func TestStatusBuilderBoundsSortsAndSummarizesFindings(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	builder := NewStatusBuilder(func() time.Time { return now })
	findings := make([]guardplatformv1alpha1.OwnershipFinding, 105)
	for i := range findings {
		findings[i] = guardplatformv1alpha1.OwnershipFinding{
			ID:         fmt.Sprintf("finding-%03d-%s", 104-i, strings.Repeat("x", 140)),
			Detector:   guardplatformv1alpha1.DetectorArgoPruneRisk,
			Target:     guardplatformv1alpha1.ResourceReference{APIGroup: "apps", Version: "v1", Kind: "ReplicaSet", Namespace: "default", Name: fmt.Sprintf("rs-%03d", i)},
			Confidence: guardplatformv1alpha1.ConfidenceConfirmed, Severity: guardplatformv1alpha1.SeverityWarning,
			EvidenceSummary: strings.Repeat("e", 600), Remediation: strings.Repeat("r", 1200),
			MissingEvidence: []guardplatformv1alpha1.MissingEvidence{guardplatformv1alpha1.MissingEvidence(strings.Repeat("m", 140)), guardplatformv1alpha1.MissingEvidence(strings.Repeat("m", 140))},
		}
	}
	snapshot := &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable}
	status := builder.BuildStatus(nil, 7, snapshot, findings, 10*time.Minute, "", "")
	if len(status.Findings) != 100 || status.TruncatedFindings != 5 || status.Summary.TotalFindings != 105 || status.Summary.Confirmed != 105 {
		t.Fatalf("unexpected bounds/summary: findings=%d truncated=%d summary=%+v", len(status.Findings), status.TruncatedFindings, status.Summary)
	}
	for _, finding := range status.Findings {
		if len(finding.ID) > 128 || len(finding.EvidenceSummary) > 512 || len(finding.Remediation) > 1024 || len(finding.MissingEvidence) != 1 || len(finding.MissingEvidence[0]) > 128 {
			t.Fatalf("finding was not bounded: %+v", finding)
		}
	}
}

func TestStatusBuilderHeartbeatAndConditionTransitionStability(t *testing.T) {
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	previousTransition := metav1.NewTime(base.Add(-time.Hour))
	previousScan := metav1.NewTime(base.Add(-5 * time.Minute))
	current := &guardplatformv1alpha1.OwnershipAuditStatus{
		LastCompletedScanTime: &previousScan,
		Conditions:            []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OldReason", LastTransitionTime: previousTransition}, {Type: "InventoryReady", Status: metav1.ConditionTrue, Reason: "OldReason", LastTransitionTime: previousTransition}},
	}
	builder := NewStatusBuilder(func() time.Time { return base })
	snapshot := &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable}
	status := builder.BuildStatus(current, 2, snapshot, nil, 10*time.Minute, "", "")
	if !status.LastCompletedScanTime.Equal(&previousScan) {
		t.Fatal("heartbeat changed before interval")
	}
	for _, condition := range status.Conditions {
		if !condition.LastTransitionTime.Equal(&previousTransition) {
			t.Fatal("reason-only change moved transition time")
		}
	}
	builder.Now = func() time.Time { return base.Add(6 * time.Minute) }
	due := builder.BuildStatus(status, 2, snapshot, nil, 10*time.Minute, "", "")
	if due.LastCompletedScanTime.Equal(status.LastCompletedScanTime) {
		t.Fatal("due heartbeat was not updated")
	}
}

func TestStatusBuilderNeverTreatsUnknownOrNilInventoryAsHealthy(t *testing.T) {
	builder := NewStatusBuilder(func() time.Time { return time.Now() })
	for name, snapshot := range map[string]*inventory.NormalizedSnapshot{"nil": nil, "unknown": {ArgoDiscoveryState: inventory.DiscoveryUnknown}} {
		t.Run(name, func(t *testing.T) {
			status := builder.BuildStatus(nil, 1, snapshot, nil, 10*time.Minute, "", "")
			for _, condition := range status.Conditions {
				if condition.Status != metav1.ConditionFalse || condition.Reason != string(inventory.ErrTransientReadFailure) {
					t.Fatalf("false healthy: %+v", condition)
				}
			}
		})
	}
}

func TestStatusBuilderTreatsArgoNotRequiredAsHealthy(t *testing.T) {
	builder := NewStatusBuilder(func() time.Time { return time.Now() })
	status := builder.BuildStatus(nil, 1, &inventory.NormalizedSnapshot{
		ArgoDiscoveryState: inventory.DiscoveryNotRequired,
	}, nil, 10*time.Minute, "", "")

	for _, condition := range status.Conditions {
		if condition.Status != metav1.ConditionTrue || condition.Reason != "InventoryCollected" {
			t.Fatalf("stale-only inventory must be healthy without Argo: %+v", condition)
		}
	}
}

func TestStatusBuilderMapsEvidenceFailures(t *testing.T) {
	builder := NewStatusBuilder(time.Now)
	cases := []struct {
		name     string
		snapshot *inventory.NormalizedSnapshot
		reason   string
	}{
		{name: "forbidden", snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable, Applications: []inventory.ApplicationEvidence{{Metadata: inventory.ObservationMetadata{SourceError: &inventory.ErrorDTO{Class: inventory.ErrEvidenceForbidden, Message: "forbidden"}}}}}, reason: string(inventory.ErrEvidenceForbidden)},
		{name: "malformed", snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable, Applications: []inventory.ApplicationEvidence{{Metadata: inventory.ObservationMetadata{SourceError: &inventory.ErrorDTO{Class: inventory.ErrMalformedEvidence, Message: "malformed"}}}}}, reason: string(inventory.ErrMalformedEvidence)},
		{name: "stale", snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable, Applications: []inventory.ApplicationEvidence{{Metadata: inventory.ObservationMetadata{Freshness: inventory.FreshnessStale}}}}, reason: string(inventory.ErrStaleEvidence)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := builder.BuildStatus(nil, 3, tc.snapshot, nil, 10*time.Minute, "", "")
			if status.ObservedGeneration != 3 || status.Conditions[0].Status != metav1.ConditionFalse || status.Conditions[0].Reason != tc.reason {
				t.Fatalf("unexpected failure mapping: %+v", status)
			}
		})
	}
}

func TestSemanticEqualStatusIgnoresOrderAndTimestamps(t *testing.T) {
	now := metav1.Now()
	left := &guardplatformv1alpha1.OwnershipAuditStatus{ObservedGeneration: 1, LastCompletedScanTime: &now, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: now}, {Type: "InventoryReady", Status: metav1.ConditionTrue, LastTransitionTime: now}}}
	later := metav1.NewTime(now.Add(time.Hour))
	right := left.DeepCopy()
	right.LastCompletedScanTime = &later
	right.Conditions[0], right.Conditions[1] = right.Conditions[1], right.Conditions[0]
	right.Conditions[0].LastTransitionTime = later
	if !SemanticEqualStatus(left, right) {
		t.Fatal("timestamp/order-only status change must be semantically equal")
	}
	right.Conditions[0].Reason = "Changed"
	if SemanticEqualStatus(left, right) {
		t.Fatal("reason change must not be semantically equal")
	}
}
