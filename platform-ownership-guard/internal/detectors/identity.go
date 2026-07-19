package detectors

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

const DefaultRuleVersion = "v1"

// BuildFindingID creates a deterministic, versioned SHA-256 hash identity for a finding.
// It excludes timestamps, resourceVersions, confidence, and message/remediation text.
func BuildFindingID(ruleVersion string, detector guardplatformv1alpha1.DetectorType, target guardplatformv1alpha1.ResourceReference, findingClass string, sourceContext string) string {
	if ruleVersion == "" {
		ruleVersion = DefaultRuleVersion
	}
	raw := fmt.Sprintf("%d:%s|%d:%s|%d:%s|%d:%s|%d:%s|%d:%s|%d:%s|%d:%s|%d:%s",
		len(ruleVersion), ruleVersion,
		len(string(detector)), string(detector),
		len(target.APIGroup), target.APIGroup,
		len(target.Version), target.Version,
		len(target.Kind), target.Kind,
		len(target.Namespace), target.Namespace,
		len(target.Name), target.Name,
		len(findingClass), findingClass,
		len(sourceContext), sourceContext,
	)
	hash := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(hash[:])
}
