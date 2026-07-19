package detectors

import (
	"fmt"
	"strings"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

const (
	FindingClassOwnerUIDMismatch         = "OwnerUIDMismatch"
	FindingClassOwnerMissing             = "OwnerMissing"
	FindingClassOwnerEvidenceUnavailable = "OwnerEvidenceUnavailable"
	RemediationStaleOwnerReference       = "Kiểm tra owner GVK/name/UID và controller chịu trách nhiệm. Xác định owner vừa bị recreate, CRD bị remove hay dependent bị orphan. Sửa/reconcile source controller; không patch UID thủ công."
)

// EvaluateStaleOwnerReference evaluates ownerReference evidence for missing or UID-mismatched owners.
func EvaluateStaleOwnerReference(snapshot *inventory.NormalizedSnapshot) []guardplatformv1alpha1.OwnershipFinding {
	if snapshot == nil {
		return nil
	}

	var findings []guardplatformv1alpha1.OwnershipFinding

	for _, owner := range snapshot.Owners {
		targetRef := toResourceReference(owner.DependentIdentity)
		sourceContext := owner.OwnerRefGVK.String() + "/" + owner.OwnerName

		switch owner.LookupResult {
		case inventory.OwnerResolved:
			// If observed owner UID is empty, observation is incomplete -> InsufficientEvidence
			if owner.ObservedOwnerUID == "" {
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorStaleOwnerReference, targetRef, FindingClassOwnerEvidenceUnavailable, sourceContext)
				summary := fmt.Sprintf("Dependent %s/%s ownerReference %s/%s resolved but observed owner UID is empty",
					owner.DependentIdentity.Kind, owner.DependentIdentity.Name,
					owner.OwnerRefGVK.Kind, owner.OwnerName)

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorStaleOwnerReference,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceInsufficientEvidence,
					Severity:        guardplatformv1alpha1.SeverityInfo,
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"ObservedOwnerUID"},
					Remediation:     RemediationStaleOwnerReference,
				})
				continue
			}

			// Match -> Clean (NoFinding)
			if owner.ObservedOwnerUID == owner.OwnerUID {
				continue
			}

			// Mismatch -> Confirmed OwnerUIDMismatch
			if owner.ObservedOwnerUID != owner.OwnerUID {
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorStaleOwnerReference, targetRef, FindingClassOwnerUIDMismatch, sourceContext)
				summary := fmt.Sprintf("Dependent %s/%s ownerReference %s/%s UID mismatch (expected \"%s\", observed \"%s\")",
					owner.DependentIdentity.Kind, owner.DependentIdentity.Name,
					owner.OwnerRefGVK.Kind, owner.OwnerName,
					string(owner.OwnerUID), string(owner.ObservedOwnerUID))

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorStaleOwnerReference,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceConfirmed,
					Severity:        guardplatformv1alpha1.SeverityWarning,
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: nil,
					Remediation:     RemediationStaleOwnerReference,
				})
			}

		case inventory.OwnerNotFound:
			// Authoritative direct lookup returned NotFound -> Confirmed OwnerMissing
			id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorStaleOwnerReference, targetRef, FindingClassOwnerMissing, sourceContext)
			summary := fmt.Sprintf("Dependent %s/%s ownerReference %s/%s authoritative lookup returned NotFound",
				owner.DependentIdentity.Kind, owner.DependentIdentity.Name,
				owner.OwnerRefGVK.Kind, owner.OwnerName)

			findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
				ID:              id,
				Detector:        guardplatformv1alpha1.DetectorStaleOwnerReference,
				Target:          targetRef,
				Confidence:      guardplatformv1alpha1.ConfidenceConfirmed,
				Severity:        guardplatformv1alpha1.SeverityWarning,
				EvidenceSummary: truncateSummary(summary),
				MissingEvidence: nil,
				Remediation:     RemediationStaleOwnerReference,
			})

		case inventory.OwnerForbidden, inventory.OwnerUnknown:
			// Cached miss, Forbidden, or Unknown error -> InsufficientEvidence OwnerEvidenceUnavailable
			id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorStaleOwnerReference, targetRef, FindingClassOwnerEvidenceUnavailable, sourceContext)
			reason := string(owner.LookupResult)
			if owner.Metadata.SourceError != nil {
				reason = strings.TrimSpace(owner.Metadata.SourceError.Message)
			}
			summary := fmt.Sprintf("Dependent %s/%s ownerReference %s/%s lookup result unavailable (%s)",
				owner.DependentIdentity.Kind, owner.DependentIdentity.Name,
				owner.OwnerRefGVK.Kind, owner.OwnerName, reason)

			findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
				ID:              id,
				Detector:        guardplatformv1alpha1.DetectorStaleOwnerReference,
				Target:          targetRef,
				Confidence:      guardplatformv1alpha1.ConfidenceInsufficientEvidence,
				Severity:        guardplatformv1alpha1.SeverityInfo,
				EvidenceSummary: truncateSummary(summary),
				MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"AuthoritativeOwnerLookup"},
				Remediation:     RemediationStaleOwnerReference,
			})
		}
	}

	sortFindings(findings)
	return findings
}
