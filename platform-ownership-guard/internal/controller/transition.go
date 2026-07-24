package controller

import (
	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

// TransitionType represents the lifecycle transition of a finding between scan cycles.
type TransitionType string

const (
	TransitionAdded     TransitionType = "added"
	TransitionChanged   TransitionType = "changed"
	TransitionResolved  TransitionType = "resolved"
	TransitionUnchanged TransitionType = "unchanged"
)

// FindingTransition represents a single finding's lifecycle change.
type FindingTransition struct {
	Type            TransitionType
	Finding         guardplatformv1alpha1.OwnershipFinding
	PreviousFinding *guardplatformv1alpha1.OwnershipFinding
}

// ComputeFindingTransitions compares old persisted findings with new desired findings.
// If inventoryReady is false, ALL transitions (Added, Changed, Resolved) are suppressed to prevent false alerts on broken evidence.
func ComputeFindingTransitions(oldFindings, newFindings []guardplatformv1alpha1.OwnershipFinding, inventoryReady bool) []FindingTransition {
	if !inventoryReady {
		return nil
	}

	oldMap := make(map[string]guardplatformv1alpha1.OwnershipFinding, len(oldFindings))
	for _, f := range oldFindings {
		if f.ID != "" {
			oldMap[f.ID] = f
		}
	}

	newMap := make(map[string]guardplatformv1alpha1.OwnershipFinding, len(newFindings))
	for _, f := range newFindings {
		if f.ID != "" {
			newMap[f.ID] = f
		}
	}

	var transitions []FindingTransition

	// Check new findings against old findings (Added, Changed, Unchanged)
	for _, newF := range newFindings {
		if newF.ID == "" {
			continue
		}
		oldF, exists := oldMap[newF.ID]
		if !exists {
			transitions = append(transitions, FindingTransition{
				Type:    TransitionAdded,
				Finding: newF,
			})
			continue
		}

		// Present in both: check for semantic changes (Severity or Confidence)
		if oldF.Severity != newF.Severity || oldF.Confidence != newF.Confidence {
			oldCopy := oldF
			transitions = append(transitions, FindingTransition{
				Type:            TransitionChanged,
				Finding:         newF,
				PreviousFinding: &oldCopy,
			})
		} else {
			oldCopy := oldF
			transitions = append(transitions, FindingTransition{
				Type:            TransitionUnchanged,
				Finding:         newF,
				PreviousFinding: &oldCopy,
			})
		}
	}

	// Check old findings missing in new findings (Resolved)
	// Suppressed when inventory is not ready to avoid false recovery claims during evidence collection failures.
	if inventoryReady {
		for _, oldF := range oldFindings {
			if oldF.ID == "" {
				continue
			}
			if _, exists := newMap[oldF.ID]; !exists {
				oldCopy := oldF
				transitions = append(transitions, FindingTransition{
					Type:            TransitionResolved,
					Finding:         oldCopy,
					PreviousFinding: &oldCopy,
				})
			}
		}
	}

	return transitions
}
