package controller_test

import (
	"testing"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/controller"
)

func TestComputeFindingTransitions(t *testing.T) {
	findingAWarning := guardplatformv1alpha1.OwnershipFinding{
		ID:         "sha256:aaa",
		Detector:   guardplatformv1alpha1.DetectorArgoPruneRisk,
		Confidence: guardplatformv1alpha1.ConfidenceSuspected,
		Severity:   guardplatformv1alpha1.SeverityWarning,
	}
	findingAHigh := guardplatformv1alpha1.OwnershipFinding{
		ID:         "sha256:aaa",
		Detector:   guardplatformv1alpha1.DetectorArgoPruneRisk,
		Confidence: guardplatformv1alpha1.ConfidenceConfirmed,
		Severity:   guardplatformv1alpha1.SeverityHigh,
	}
	findingB := guardplatformv1alpha1.OwnershipFinding{
		ID:         "sha256:bbb",
		Detector:   guardplatformv1alpha1.DetectorStaleOwnerReference,
		Confidence: guardplatformv1alpha1.ConfidenceConfirmed,
		Severity:   guardplatformv1alpha1.SeverityWarning,
	}
	findingC := guardplatformv1alpha1.OwnershipFinding{
		ID:         "sha256:ccc",
		Detector:   guardplatformv1alpha1.DetectorArgoPruneRisk,
		Confidence: guardplatformv1alpha1.ConfidenceConfirmed,
		Severity:   guardplatformv1alpha1.SeverityInfo,
	}

	// 1. Added transition
	t.Run("Added", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{}
		newList := []guardplatformv1alpha1.OwnershipFinding{findingAWarning}
		tr := controller.ComputeFindingTransitions(oldList, newList, true)
		if len(tr) != 1 || tr[0].Type != controller.TransitionAdded {
			t.Fatalf("expected 1 Added transition, got %+v", tr)
		}
	})

	// 2. Changed transition
	t.Run("Changed", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{findingAWarning}
		newList := []guardplatformv1alpha1.OwnershipFinding{findingAHigh}
		tr := controller.ComputeFindingTransitions(oldList, newList, true)
		if len(tr) != 1 || tr[0].Type != controller.TransitionChanged {
			t.Fatalf("expected 1 Changed transition, got %+v", tr)
		}
	})

	// 3. Unchanged transition
	t.Run("Unchanged", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{findingAHigh}
		newList := []guardplatformv1alpha1.OwnershipFinding{findingAHigh}
		tr := controller.ComputeFindingTransitions(oldList, newList, true)
		if len(tr) != 1 || tr[0].Type != controller.TransitionUnchanged {
			t.Fatalf("expected 1 Unchanged transition, got %+v", tr)
		}
	})

	// 4. Resolved transition when inventory is ready
	t.Run("ResolvedWhenInventoryReady", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{findingB}
		newList := []guardplatformv1alpha1.OwnershipFinding{}
		tr := controller.ComputeFindingTransitions(oldList, newList, true)
		if len(tr) != 1 || tr[0].Type != controller.TransitionResolved {
			t.Fatalf("expected 1 Resolved transition, got %+v", tr)
		}
	})

	// 5. Transitions SUPPRESSED when inventory is NOT ready
	t.Run("AllTransitionsSuppressedWhenInventoryUnhealthy", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{findingB}
		newList := []guardplatformv1alpha1.OwnershipFinding{findingAWarning, findingC}
		tr := controller.ComputeFindingTransitions(oldList, newList, false) // inventoryReady = false
		if len(tr) != 0 {
			t.Fatalf("expected 0 transitions when inventory unhealthy, got %d", len(tr))
		}
	})

	// 6. Mixed transitions (A Changed, B Resolved, C Added)
	t.Run("MixedTransitions", func(t *testing.T) {
		oldList := []guardplatformv1alpha1.OwnershipFinding{findingAWarning, findingB}
		newList := []guardplatformv1alpha1.OwnershipFinding{findingAHigh, findingC}
		tr := controller.ComputeFindingTransitions(oldList, newList, true)
		if len(tr) != 3 {
			t.Fatalf("expected 3 transitions, got %d", len(tr))
		}

		types := make(map[controller.TransitionType]int)
		for _, item := range tr {
			types[item.Type]++
		}
		if types[controller.TransitionAdded] != 1 || types[controller.TransitionChanged] != 1 || types[controller.TransitionResolved] != 1 {
			t.Errorf("unexpected transition type counts: %+v", types)
		}
	})
}
