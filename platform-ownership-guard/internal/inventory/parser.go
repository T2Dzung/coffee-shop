package inventory

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SafeParser extracts fields defensively from unstructured objects.
type SafeParser struct{}

// NewSafeParser creates a new SafeParser.
func NewSafeParser() *SafeParser {
	return &SafeParser{}
}

// ParseApplication converts an unstructured Argo CD Application CR into ApplicationEvidence.
func (p *SafeParser) ParseApplication(unstr *unstructured.Unstructured) (*ApplicationEvidence, error) {
	if unstr == nil {
		return nil, fmt.Errorf("nil unstructured object")
	}

	appRef := ResourceIdentity{
		APIGroup:  unstr.GroupVersionKind().Group,
		Version:   unstr.GroupVersionKind().Version,
		Kind:      unstr.GroupVersionKind().Kind,
		Namespace: unstr.GetNamespace(),
		Name:      unstr.GetName(),
		UID:       unstr.GetUID(),
	}

	// Missing optional fields are valid; wrong-type fields are malformed evidence.
	syncStatus, syncStatusKnown, err := unstructured.NestedString(unstr.Object, "status", "sync", "status")
	if err != nil {
		return nil, fmt.Errorf("status.sync.status is not a string: %w", err)
	}

	// spec.syncPolicy.automated.prune
	autoPrune := false
	pruneVal, found, err := unstructured.NestedFieldNoCopy(unstr.Object, "spec", "syncPolicy", "automated")
	if err != nil {
		return nil, fmt.Errorf("spec.syncPolicy.automated is malformed: %w", err)
	}
	if found && pruneVal != nil {
		if pruneMap, ok := pruneVal.(map[string]interface{}); ok {
			if prune, exists := pruneMap["prune"]; exists {
				if pruneBool, ok := prune.(bool); ok {
					autoPrune = pruneBool
				} else {
					return nil, fmt.Errorf("spec.syncPolicy.automated.prune is not a boolean")
				}
			}
		} else {
			return nil, fmt.Errorf("spec.syncPolicy.automated is not a map")
		}
	}

	// status.resources list
	var resourceIdentities []ResourceIdentity
	resourcesVal, found, err := unstructured.NestedFieldNoCopy(unstr.Object, "status", "resources")
	if err != nil {
		return nil, fmt.Errorf("status.resources is malformed: %w", err)
	}
	if found && resourcesVal != nil {
		resourcesList, ok := resourcesVal.([]interface{})
		if !ok {
			return nil, fmt.Errorf("status.resources is not a list")
		}

		for i, res := range resourcesList {
			resMap, ok := res.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("status.resources[%d] is not a map", i)
			}

			group, _, err := readStringField(resMap, "group")
			if err != nil {
				return nil, fmt.Errorf("status.resources[%d].group: %w", i, err)
			}
			kind, _, err := readStringField(resMap, "kind")
			if err != nil {
				return nil, fmt.Errorf("status.resources[%d].kind: %w", i, err)
			}
			namespace, _, err := readStringField(resMap, "namespace")
			if err != nil {
				return nil, fmt.Errorf("status.resources[%d].namespace: %w", i, err)
			}
			name, _, err := readStringField(resMap, "name")
			if err != nil {
				return nil, fmt.Errorf("status.resources[%d].name: %w", i, err)
			}
			version, _, err := readStringField(resMap, "version")
			if err != nil {
				return nil, fmt.Errorf("status.resources[%d].version: %w", i, err)
			}

			if kind == "" || name == "" {
				return nil, fmt.Errorf("status.resources[%d] missing kind or name", i)
			}

			resourceIdentities = append(resourceIdentities, ResourceIdentity{
				APIGroup:  group,
				Version:   version,
				Kind:      kind,
				Namespace: namespace,
				Name:      name,
			})
		}
	}

	evidence := &ApplicationEvidence{
		ApplicationRef:     appRef,
		SyncStatus:         syncStatus,
		SyncStatusKnown:    syncStatusKnown,
		AutoPruneKnown:     true,
		AutoPruneEnabled:   autoPrune,
		ResourceIdentities: resourceIdentities,
	}
	if err := p.ParseApplicationObservation(unstr, evidence); err != nil {
		return nil, err
	}

	return evidence, nil
}

// ParseApplicationObservation extracts freshness and a bounded condition summary.
func (p *SafeParser) ParseApplicationObservation(unstr *unstructured.Unstructured, evidence *ApplicationEvidence) error {
	reconciledAt, found, err := unstructured.NestedString(unstr.Object, "status", "reconciledAt")
	if err != nil {
		return fmt.Errorf("status.reconciledAt is not a string: %w", err)
	}
	if found && reconciledAt != "" {
		parsed, err := time.Parse(time.RFC3339, reconciledAt)
		if err != nil {
			return fmt.Errorf("status.reconciledAt is not RFC3339: %w", err)
		}
		evidence.Metadata.SourceObservedAt = &parsed
	}
	conditions, found, err := unstructured.NestedSlice(unstr.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("status.conditions is not a list: %w", err)
	}
	if !found {
		return nil
	}
	for i, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("status.conditions[%d] is not a map", i)
		}
		conditionType, typeFound, err := unstructured.NestedString(condition, "type")
		if err != nil || !typeFound || conditionType == "" {
			return fmt.Errorf("status.conditions[%d].type is missing or not a string", i)
		}
		message, _, err := unstructured.NestedString(condition, "message")
		if err != nil {
			return fmt.Errorf("status.conditions[%d].message is not a string", i)
		}
		if len(message) > 512 {
			message = message[:512]
		}
		evidence.SourceConditions = append(evidence.SourceConditions, SourceCondition{Type: conditionType, Message: message})
	}
	return nil
}

func readStringField(object map[string]interface{}, field string) (string, bool, error) {
	value, found, err := unstructured.NestedString(object, field)
	if err != nil {
		return "", found, fmt.Errorf("must be a string: %w", err)
	}
	return value, found, nil
}

// ParseProtection extracts compare and sync options annotations from target resources.
func (p *SafeParser) ParseProtection(unstr *unstructured.Unstructured) *ProtectionEvidence {
	ref := ResourceIdentity{
		APIGroup:  unstr.GroupVersionKind().Group,
		Version:   unstr.GroupVersionKind().Version,
		Kind:      unstr.GroupVersionKind().Kind,
		Namespace: unstr.GetNamespace(),
		Name:      unstr.GetName(),
		UID:       unstr.GetUID(),
	}

	annotations := unstr.GetAnnotations()
	pruneFalse := false

	if val, ok := annotations["argocd.argoproj.io/compare-options"]; ok {
		if strings.Contains(val, "IgnoreExtraneous") {
			pruneFalse = true
		}
	}

	if val, ok := annotations["argocd.argoproj.io/sync-options"]; ok {
		if strings.Contains(val, "Prune=false") {
			pruneFalse = true
		}
	}

	return &ProtectionEvidence{
		TargetRef:       ref,
		Readable:        true,
		PruneFalseKnown: true,
		PruneFalse:      pruneFalse,
	}
}
