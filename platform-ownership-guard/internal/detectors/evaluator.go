package detectors

import (
	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

// RealEvaluator implements the FoundationEvaluator interface for production.
type RealEvaluator struct{}

// NewEvaluator creates a new RealEvaluator.
func NewEvaluator() *RealEvaluator {
	return &RealEvaluator{}
}

// Evaluate runs enabled pure detectors against a normalized snapshot in fixed enum order.
func (e *RealEvaluator) Evaluate(snapshot *inventory.NormalizedSnapshot, enabled []guardplatformv1alpha1.DetectorType) []guardplatformv1alpha1.OwnershipFinding {
	if snapshot == nil || len(enabled) == 0 {
		return nil
	}

	// Deduplicate enabled detector types while preserving fixed enum order
	enabledSet := make(map[guardplatformv1alpha1.DetectorType]bool)
	for _, d := range enabled {
		enabledSet[d] = true
	}

	var allFindings []guardplatformv1alpha1.OwnershipFinding

	// Dispatch in fixed enum order: ArgoPruneRisk, then StaleOwnerReference
	if enabledSet[guardplatformv1alpha1.DetectorArgoPruneRisk] {
		findings := EvaluateArgoPruneRisk(snapshot)
		allFindings = append(allFindings, findings...)
	}

	if enabledSet[guardplatformv1alpha1.DetectorStaleOwnerReference] {
		findings := EvaluateStaleOwnerReference(snapshot)
		allFindings = append(allFindings, findings...)
	}

	// Deduplicate findings by stable ID
	seenIDs := make(map[string]bool)
	var deduped []guardplatformv1alpha1.OwnershipFinding
	for _, f := range allFindings {
		if !seenIDs[f.ID] {
			seenIDs[f.ID] = true
			deduped = append(deduped, f)
		}
	}

	sortFindings(deduped)
	return deduped
}
