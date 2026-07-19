package detectors

import (
	"fmt"
	"sort"
	"strings"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

const (
	FindingClassPruneRisk              = "PruneRisk"
	FindingClassAppEvidenceUnavailable = "ApplicationEvidenceUnavailable"
	RemediationArgoPruneRisk           = "Xác định controller/source tạo resource và Application đang track nó. Kiểm tra requiresPruning, tracking metadata và argocd.argoproj.io/sync-options: Prune=false tại source of truth. Dùng IgnoreExtraneous chỉ để điều chỉnh comparison nếu phù hợp; nó không chặn prune. Không manual patch rồi coi drift đã được giải quyết."
)

// EvaluateArgoPruneRisk evaluates normalized evidence for Argo CD prune risks.
func EvaluateArgoPruneRisk(snapshot *inventory.NormalizedSnapshot) []guardplatformv1alpha1.OwnershipFinding {
	if snapshot == nil {
		return nil
	}

	// Index protections by canonical target identity key
	protectionMap := make(map[string]inventory.ProtectionEvidence)
	for _, prot := range snapshot.Protections {
		key := resourceIdentityKey(prot.TargetRef)
		protectionMap[key] = prot
	}

	var findings []guardplatformv1alpha1.OwnershipFinding

	for _, app := range snapshot.Applications {
		sourceContext := app.ApplicationRef.Namespace + "/" + app.ApplicationRef.Name

		// 1. Handle Application-level source errors or stale freshness
		if app.Metadata.SourceError != nil || app.Metadata.Freshness == inventory.FreshnessStale {
			for _, res := range app.Resources {
				if !isResourceInAuditScope(snapshot, res.Identity) {
					continue
				}
				targetRef := toResourceReference(res.Identity)
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorArgoPruneRisk, targetRef, FindingClassAppEvidenceUnavailable, sourceContext)

				summary := fmt.Sprintf("Application \"%s\" evidence unavailable or stale for resource %s/%s", sourceContext, res.Identity.Kind, res.Identity.Name)
				if app.Metadata.SourceError != nil {
					summary = fmt.Sprintf("Application \"%s\" evidence read failed (%s) for resource %s/%s", sourceContext, app.Metadata.SourceError.Class, res.Identity.Kind, res.Identity.Name)
				}

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorArgoPruneRisk,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceInsufficientEvidence,
					Severity:        guardplatformv1alpha1.SeverityInfo,
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"FreshApplicationEvidence"},
					Remediation:     RemediationArgoPruneRisk,
				})
			}
			continue
		}

		// Check for untrustworthy Application status (blocking conditions or unknown freshness)
		isUntrustworthyAppStatus := app.Metadata.Freshness != inventory.FreshnessFresh || hasBlockingConditions(app)

		// 2. Evaluate resources marked requiresPruning
		for _, res := range app.Resources {
			if !res.RequiresPruning || !isResourceInAuditScope(snapshot, res.Identity) {
				continue
			}

			targetRef := toResourceReference(res.Identity)
			key := resourceIdentityKey(res.Identity)
			prot, isProtectionKnown := protectionMap[key]

			// Case A: The Application cannot currently support a risk/clean conclusion.
			if isUntrustworthyAppStatus {
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorArgoPruneRisk, targetRef, FindingClassAppEvidenceUnavailable, sourceContext)
				summary := fmt.Sprintf("Application \"%s\" status is not trustworthy enough to evaluate prune risk for %s/%s", sourceContext, res.Identity.Kind, res.Identity.Name)

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorArgoPruneRisk,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceInsufficientEvidence,
					Severity:        guardplatformv1alpha1.SeverityInfo,
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"TrustworthyApplicationStatus"},
					Remediation:     RemediationArgoPruneRisk,
				})
				continue
			}

			// Case B: Kind Secret (protection unreadable by design in Guard)
			if res.Identity.Kind == "Secret" {
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorArgoPruneRisk, targetRef, FindingClassPruneRisk, sourceContext)
				summary := fmt.Sprintf("Secret \"%s/%s\" reported as requiresPruning by Argo Application \"%s\"; protection unreadable by design", res.Identity.Namespace, res.Identity.Name, sourceContext)

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorArgoPruneRisk,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceSuspected,
					Severity:        guardplatformv1alpha1.SeverityHigh,
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"SecretProtectionUnreadable"},
					Remediation:     RemediationArgoPruneRisk,
				})
				continue
			}

			// Case C: Target readable & has explicit PruneFalse -> Clean (NoFinding)
			if isProtectionKnown && prot.Readable && prot.PruneFalse {
				continue
			}

			// Case D: Protection missing or unreadable -> MUST be Suspected (never Confirmed)
			if !isProtectionKnown || !prot.Readable || !prot.PruneFalseKnown {
				id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorArgoPruneRisk, targetRef, FindingClassPruneRisk, sourceContext)
				summary := fmt.Sprintf("%s \"%s/%s\" reported as requiresPruning by Argo Application \"%s\"; target protection unreadable or unknown", res.Identity.Kind, res.Identity.Namespace, res.Identity.Name, sourceContext)

				findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
					ID:              id,
					Detector:        guardplatformv1alpha1.DetectorArgoPruneRisk,
					Target:          targetRef,
					Confidence:      guardplatformv1alpha1.ConfidenceSuspected,
					Severity:        calculateSeverity(res.Identity.Kind),
					EvidenceSummary: truncateSummary(summary),
					MissingEvidence: []guardplatformv1alpha1.MissingEvidence{"TargetProtectionReadable"},
					Remediation:     RemediationArgoPruneRisk,
				})
				continue
			}

			// Case E: Target readable, protection known & readable, no Prune=false
			var confidence guardplatformv1alpha1.FindingConfidence
			var severity guardplatformv1alpha1.FindingSeverity
			var missingEvidence []guardplatformv1alpha1.MissingEvidence
			var summary string

			if app.AutoPruneKnown && app.AutoPruneEnabled {
				confidence = guardplatformv1alpha1.ConfidenceConfirmed
				severity = calculateSeverity(res.Identity.Kind)
				summary = fmt.Sprintf("%s \"%s/%s\" reported as requiresPruning by Argo Application \"%s\" with auto-prune enabled", res.Identity.Kind, res.Identity.Namespace, res.Identity.Name, sourceContext)
			} else {
				confidence = guardplatformv1alpha1.ConfidenceSuspected
				severity = calculateSeverity(res.Identity.Kind)
				missingEvidence = []guardplatformv1alpha1.MissingEvidence{"AutoPruneStatusUnconfirmed"}
				summary = fmt.Sprintf("%s \"%s/%s\" reported as requiresPruning by Argo Application \"%s\" with auto-prune disabled or unconfirmed", res.Identity.Kind, res.Identity.Namespace, res.Identity.Name, sourceContext)
			}

			if prot.IgnoreExtraneous {
				summary += "; CompareOptions IgnoreExtraneous present but does not protect against sync-prune"
			}

			id := BuildFindingID(DefaultRuleVersion, guardplatformv1alpha1.DetectorArgoPruneRisk, targetRef, FindingClassPruneRisk, sourceContext)
			findings = append(findings, guardplatformv1alpha1.OwnershipFinding{
				ID:              id,
				Detector:        guardplatformv1alpha1.DetectorArgoPruneRisk,
				Target:          targetRef,
				Confidence:      confidence,
				Severity:        severity,
				EvidenceSummary: truncateSummary(summary),
				MissingEvidence: missingEvidence,
				Remediation:     RemediationArgoPruneRisk,
			})
		}
	}

	sortFindings(findings)
	return findings
}

func hasBlockingConditions(app inventory.ApplicationEvidence) bool {
	for _, cond := range app.SourceConditions {
		if cond.Type == "ComparisonError" || cond.Type == "InvalidSpecError" || cond.Type == "UnknownError" {
			return true
		}
	}
	return false
}

func calculateSeverity(kind string) guardplatformv1alpha1.FindingSeverity {
	if kind == "Deployment" || kind == "Secret" || kind == "PersistentVolumeClaim" {
		return guardplatformv1alpha1.SeverityHigh
	}
	return guardplatformv1alpha1.SeverityWarning
}

func resourceIdentityKey(ref inventory.ResourceIdentity) string {
	return ref.APIGroup + "/" + ref.Version + "/" + ref.Kind + "/" + ref.Namespace + "/" + ref.Name
}

func toResourceReference(ref inventory.ResourceIdentity) guardplatformv1alpha1.ResourceReference {
	return guardplatformv1alpha1.ResourceReference{
		APIGroup:  ref.APIGroup,
		Version:   ref.Version,
		Kind:      ref.Kind,
		Namespace: ref.Namespace,
		Name:      ref.Name,
		UID:       ref.UID,
	}
}

// isResourceInAuditScope prevents namespaced status from inventing identities for
// cluster-scoped, cross-namespace, or malformed Argo resource entries.
func isResourceInAuditScope(snapshot *inventory.NormalizedSnapshot, ref inventory.ResourceIdentity) bool {
	if ref.Version == "" || ref.Kind == "" || ref.Namespace == "" || ref.Name == "" {
		return false
	}
	return snapshot.TargetNamespace == "" || ref.Namespace == snapshot.TargetNamespace
}

func truncateSummary(summary string) string {
	summary = strings.Join(strings.Fields(summary), " ")
	if len(summary) > 512 {
		return summary[:512]
	}
	return summary
}

func sortFindings(findings []guardplatformv1alpha1.OwnershipFinding) {
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].ID < findings[j].ID
	})
}
