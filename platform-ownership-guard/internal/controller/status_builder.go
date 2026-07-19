package controller

import (
	"reflect"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

// FoundationEvaluator is the pure seam Phase 6.5 detectors implement.
type FoundationEvaluator interface {
	Evaluate(snapshot *inventory.NormalizedSnapshot, enabled []guardplatformv1alpha1.DetectorType) []guardplatformv1alpha1.OwnershipFinding
}

// NoopFoundationEvaluator deliberately makes no business inference.
type NoopFoundationEvaluator struct{}

func (NoopFoundationEvaluator) Evaluate(_ *inventory.NormalizedSnapshot, _ []guardplatformv1alpha1.DetectorType) []guardplatformv1alpha1.OwnershipFinding {
	return nil
}

// StatusBuilder manages conditions, status bounds, sorting, and heartbeat policy.
type StatusBuilder struct {
	Now func() time.Time
}

func NewStatusBuilder(now func() time.Time) *StatusBuilder {
	if now == nil {
		now = time.Now
	}
	return &StatusBuilder{Now: now}
}

func (b *StatusBuilder) BuildStatus(
	currentStatus *guardplatformv1alpha1.OwnershipAuditStatus,
	generation int64,
	snapshot *inventory.NormalizedSnapshot,
	findings []guardplatformv1alpha1.OwnershipFinding,
	resyncInterval time.Duration,
	overrideReason string,
	overrideMessage string,
) *guardplatformv1alpha1.OwnershipAuditStatus {
	if currentStatus == nil {
		currentStatus = &guardplatformv1alpha1.OwnershipAuditStatus{}
	}
	if resyncInterval <= 0 {
		resyncInterval = 10 * time.Minute
	}
	now := b.Now()
	nowTime := metav1.NewTime(now)

	readyStatus := metav1.ConditionTrue
	inventoryReadyStatus := metav1.ConditionTrue
	reason := "InventoryCollected"
	message := "Inventory collected and normalized successfully"

	if overrideReason != "" {
		readyStatus = metav1.ConditionFalse
		inventoryReadyStatus = metav1.ConditionFalse
		reason = overrideReason
		message = overrideMessage
	} else if failureReason, failureMessage := snapshotFailure(snapshot); failureReason != "" {
		readyStatus = metav1.ConditionFalse
		inventoryReadyStatus = metav1.ConditionFalse
		reason = failureReason
		message = failureMessage
	}

	message = truncateString(message, 512)
	boundedFindings, truncated := BoundFindings(findings)
	summary := summarizeFindings(findings)
	if readyStatus != metav1.ConditionTrue && summary.InsufficientEvidence == 0 {
		summary.InsufficientEvidence = 1
	}

	status := &guardplatformv1alpha1.OwnershipAuditStatus{
		ObservedGeneration:    generation,
		LastCompletedScanTime: currentStatus.LastCompletedScanTime,
		Conditions: []metav1.Condition{
			b.buildOrUpdateCondition(currentStatus.Conditions, "Ready", readyStatus, reason, message, generation, nowTime),
			b.buildOrUpdateCondition(currentStatus.Conditions, "InventoryReady", inventoryReadyStatus, reason, message, generation, nowTime),
		},
		Summary:           summary,
		Findings:          boundedFindings,
		TruncatedFindings: truncated,
	}

	if readyStatus == metav1.ConditionTrue &&
		(currentStatus.LastCompletedScanTime == nil || now.Sub(currentStatus.LastCompletedScanTime.Time) >= resyncInterval) {
		status.LastCompletedScanTime = &nowTime
	}
	return status
}

func snapshotFailure(snapshot *inventory.NormalizedSnapshot) (string, string) {
	if snapshot == nil {
		return string(inventory.ErrTransientReadFailure), "inventory collector returned no snapshot"
	}
	switch snapshot.ArgoDiscoveryState {
	case inventory.DiscoveryUnavailable:
		return errorReason(snapshot.ArgoDiscoveryError, inventory.ErrDependencyUnavailable, "Argo Application API is unavailable")
	case inventory.DiscoveryForbidden:
		return errorReason(snapshot.ArgoDiscoveryError, inventory.ErrEvidenceForbidden, "Argo Application API discovery is forbidden")
	case inventory.DiscoveryUnknown:
		return errorReason(snapshot.ArgoDiscoveryError, inventory.ErrTransientReadFailure, "Argo Application API discovery state is unknown")
	case inventory.DiscoveryAvailable:
	default:
		return string(inventory.ErrTransientReadFailure), "Argo Application discovery state is empty or invalid"
	}

	var selected *inventory.ErrorDTO
	selectError := func(candidate *inventory.ErrorDTO) {
		if candidate != nil && (selected == nil || errorPriority(candidate.Class) > errorPriority(selected.Class)) {
			selected = candidate
		}
	}
	for i := range snapshot.Applications {
		selectError(snapshot.Applications[i].Metadata.SourceError)
		if snapshot.Applications[i].Metadata.Freshness == inventory.FreshnessStale {
			selectError(&inventory.ErrorDTO{Class: inventory.ErrStaleEvidence, Message: "Application evidence is stale"})
		}
	}
	for i := range snapshot.Protections {
		selectError(snapshot.Protections[i].Metadata.SourceError)
	}
	for i := range snapshot.Owners {
		selectError(snapshot.Owners[i].Metadata.SourceError)
	}
	if selected != nil {
		return string(selected.Class), truncateString(selected.Message, 512)
	}
	return "", ""
}

func errorReason(dto *inventory.ErrorDTO, fallback inventory.ErrorClass, message string) (string, string) {
	if dto == nil {
		return string(fallback), message
	}
	return string(dto.Class), truncateString(dto.Message, 512)
}

func errorPriority(class inventory.ErrorClass) int {
	switch class {
	case inventory.ErrInvalidInventoryScope:
		return 6
	case inventory.ErrTransientReadFailure:
		return 5
	case inventory.ErrEvidenceForbidden:
		return 4
	case inventory.ErrMalformedEvidence:
		return 3
	case inventory.ErrDependencyUnavailable:
		return 2
	case inventory.ErrStaleEvidence:
		return 1
	default:
		return 0
	}
}

func summarizeFindings(findings []guardplatformv1alpha1.OwnershipFinding) guardplatformv1alpha1.AuditSummary {
	summary := guardplatformv1alpha1.AuditSummary{TotalFindings: int32(len(findings))}
	for i := range findings {
		switch findings[i].Confidence {
		case guardplatformv1alpha1.ConfidenceConfirmed:
			summary.Confirmed++
		case guardplatformv1alpha1.ConfidenceSuspected:
			summary.Suspected++
		case guardplatformv1alpha1.ConfidenceInsufficientEvidence:
			summary.InsufficientEvidence++
		}
	}
	return summary
}

// BoundFindings sanitizes, sorts, and applies the API status budget.
func BoundFindings(input []guardplatformv1alpha1.OwnershipFinding) ([]guardplatformv1alpha1.OwnershipFinding, int32) {
	if len(input) == 0 {
		return nil, 0
	}
	findings := make([]guardplatformv1alpha1.OwnershipFinding, len(input))
	copy(findings, input)
	for i := range findings {
		findings[i].ID = truncateString(findings[i].ID, 128)
		findings[i].EvidenceSummary = truncateString(findings[i].EvidenceSummary, 512)
		findings[i].Remediation = truncateString(findings[i].Remediation, 1024)
		seen := make(map[guardplatformv1alpha1.MissingEvidence]struct{})
		boundedMissing := make([]guardplatformv1alpha1.MissingEvidence, 0, 8)
		for _, missing := range findings[i].MissingEvidence {
			missing = guardplatformv1alpha1.MissingEvidence(truncateString(string(missing), 128))
			if missing == "" {
				continue
			}
			if _, duplicate := seen[missing]; duplicate {
				continue
			}
			seen[missing] = struct{}{}
			boundedMissing = append(boundedMissing, missing)
			if len(boundedMissing) == 8 {
				break
			}
		}
		sort.Slice(boundedMissing, func(left, right int) bool { return boundedMissing[left] < boundedMissing[right] })
		findings[i].MissingEvidence = boundedMissing
	}
	SortFindings(findings)
	if len(findings) <= 100 {
		return findings, 0
	}
	return findings[:100], int32(len(findings) - 100)
}

func (b *StatusBuilder) buildOrUpdateCondition(
	existing []metav1.Condition,
	condType string,
	status metav1.ConditionStatus,
	reason string,
	message string,
	generation int64,
	nowTime metav1.Time,
) metav1.Condition {
	var oldCondition *metav1.Condition
	for i := range existing {
		if existing[i].Type == condType {
			oldCondition = &existing[i]
			break
		}
	}
	transition := nowTime
	if oldCondition != nil && oldCondition.Status == status {
		transition = oldCondition.LastTransitionTime
	}
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: transition,
	}
}

// SemanticEqualStatus ignores heartbeat and transition timestamps, and is order-insensitive.
func SemanticEqualStatus(oldStatus, newStatus *guardplatformv1alpha1.OwnershipAuditStatus) bool {
	if oldStatus == nil || newStatus == nil {
		return oldStatus == nil && newStatus == nil
	}
	oldCopy := oldStatus.DeepCopy()
	newCopy := newStatus.DeepCopy()
	oldCopy.LastCompletedScanTime = nil
	newCopy.LastCompletedScanTime = nil
	for i := range oldCopy.Conditions {
		oldCopy.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	for i := range newCopy.Conditions {
		newCopy.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	sort.Slice(oldCopy.Conditions, func(i, j int) bool { return oldCopy.Conditions[i].Type < oldCopy.Conditions[j].Type })
	sort.Slice(newCopy.Conditions, func(i, j int) bool { return newCopy.Conditions[i].Type < newCopy.Conditions[j].Type })
	SortFindings(oldCopy.Findings)
	SortFindings(newCopy.Findings)
	return reflect.DeepEqual(oldCopy, newCopy)
}

func truncateString(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func SortFindings(findings []guardplatformv1alpha1.OwnershipFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		if findings[i].Detector != findings[j].Detector {
			return findings[i].Detector < findings[j].Detector
		}
		if targetKey(findings[i].Target) != targetKey(findings[j].Target) {
			return targetKey(findings[i].Target) < targetKey(findings[j].Target)
		}
		return findings[i].ID < findings[j].ID
	})
}

func severityRank(severity guardplatformv1alpha1.FindingSeverity) int {
	switch severity {
	case guardplatformv1alpha1.SeverityHigh:
		return 3
	case guardplatformv1alpha1.SeverityWarning:
		return 2
	case guardplatformv1alpha1.SeverityInfo:
		return 1
	default:
		return 0
	}
}

func targetKey(target guardplatformv1alpha1.ResourceReference) string {
	return target.APIGroup + "/" + target.Version + "/" + target.Kind + "/" + target.Namespace + "/" + target.Name
}
