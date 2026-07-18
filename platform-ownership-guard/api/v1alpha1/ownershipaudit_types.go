/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// DetectorType identifies a detector implemented by the guard binary.
// Phase 6.4 defines the public API values; Phase 6.5 implements their rules.
// +kubebuilder:validation:Enum=ArgoPruneRisk;StaleOwnerReference
type DetectorType string

const (
	// DetectorArgoPruneRisk correlates Argo inventory with prune-risk evidence.
	DetectorArgoPruneRisk DetectorType = "ArgoPruneRisk"
	// DetectorStaleOwnerReference detects missing or UID-mismatched owners.
	DetectorStaleOwnerReference DetectorType = "StaleOwnerReference"
)

// FindingConfidence describes how completely available evidence supports a finding.
// +kubebuilder:validation:Enum=Confirmed;Suspected;InsufficientEvidence
type FindingConfidence string

const (
	// ConfidenceConfirmed means direct, fresh evidence satisfies the rule.
	ConfidenceConfirmed FindingConfidence = "Confirmed"
	// ConfidenceSuspected means correlation indicates risk but decisive evidence is missing.
	ConfidenceSuspected FindingConfidence = "Suspected"
	// ConfidenceInsufficientEvidence means the guard cannot safely conclude either way.
	ConfidenceInsufficientEvidence FindingConfidence = "InsufficientEvidence"
)

// FindingSeverity describes potential operational impact independently of confidence.
// +kubebuilder:validation:Enum=Info;Warning;High
type FindingSeverity string

const (
	// SeverityInfo is an informational ownership or evidence note.
	SeverityInfo FindingSeverity = "Info"
	// SeverityWarning requires investigation but has no proven immediate destructive path.
	SeverityWarning FindingSeverity = "Warning"
	// SeverityHigh identifies a potentially destructive path for an important resource.
	SeverityHigh FindingSeverity = "High"
)

// MissingEvidence is one bounded description of evidence unavailable to a detector.
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=128
type MissingEvidence string

// ApplicationReference identifies one explicitly selected Argo CD Application.
type ApplicationReference struct {
	// Namespace is required because Applications may live outside the audit namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace"`

	// Name is the Kubernetes name of the Application.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`
}

// TargetRule selects one explicit Group/Version/Kind supported by the guard binary.
// Core resources use an empty APIGroup. Wildcards are forbidden.
type TargetRule struct {
	// +required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^$|^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	APIGroup string `json:"apiGroup"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9]*$`
	Version string `json:"version"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9]*$`
	Kind string `json:"kind"`
}

// OwnershipAuditSpec defines the desired audit scope and cadence.
type OwnershipAuditSpec struct {
	// ApplicationRefs are explicit Argo CD Applications used as ownership evidence.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	ApplicationRefs []ApplicationReference `json:"applicationRefs"`

	// Detectors enables only rules compiled into this guard version.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +listType=set
	Detectors []DetectorType `json:"detectors"`

	// TargetRules is an explicit GVK allowlist within the audit namespace.
	// Runtime support is additionally constrained by the binary and its RBAC.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=apiGroup
	// +listMapKey=version
	// +listMapKey=kind
	TargetRules []TargetRule `json:"targetRules"`

	// ResyncInterval bounds eventual re-audit and optional API rediscovery.
	// +optional
	// +kubebuilder:default="10m"
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('5m') && duration(self) <= duration('24h')",message="resyncInterval must be between 5m and 24h"
	ResyncInterval metav1.Duration `json:"resyncInterval,omitempty"`
}

// ResourceReference is a bounded identity; it never embeds the target object.
type ResourceReference struct {
	// +required
	// +kubebuilder:validation:MaxLength=253
	APIGroup string `json:"apiGroup"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Version string `json:"version"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// UID is present when live evidence resolved the exact object identity.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	UID types.UID `json:"uid,omitempty"`
}

// OwnershipFinding is one deterministic, bounded current finding.
type OwnershipFinding struct {
	// ID is stable across equivalent reconciliations and excludes timestamps.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ID string `json:"id"`

	// +required
	Detector DetectorType `json:"detector"`

	// +required
	Target ResourceReference `json:"target"`

	// +required
	Confidence FindingConfidence `json:"confidence"`

	// +required
	Severity FindingSeverity `json:"severity"`

	// EvidenceSummary contains sanitized evidence, never a raw object or Secret value.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	EvidenceSummary string `json:"evidenceSummary"`

	// MissingEvidence lists bounded facts required to raise confidence.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	// +listType=set
	MissingEvidence []MissingEvidence `json:"missingEvidence,omitempty"`

	// Remediation is human guidance; the guard does not apply it.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Remediation string `json:"remediation"`
}

// AuditSummary stores bounded aggregate counts.
type AuditSummary struct {
	// +optional
	// +kubebuilder:validation:Minimum=0
	TotalFindings int32 `json:"totalFindings,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	Confirmed int32 `json:"confirmed,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	Suspected int32 `json:"suspected,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	InsufficientEvidence int32 `json:"insufficientEvidence,omitempty"`
}

// OwnershipAuditStatus defines the current bounded observation reported by the guard.
// +kubebuilder:validation:XValidation:rule="!has(self.conditions) || self.conditions.all(c, size(c.message) <= 512)",message="condition messages must not exceed 512 characters"
type OwnershipAuditStatus struct {
	// ObservedGeneration is the spec generation represented by this status outcome.
	// It may advance when Ready is false if the failure has been classified and persisted.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastCompletedScanTime is a bounded scheduled-scan heartbeat, not an event-reconcile timestamp.
	// +optional
	LastCompletedScanTime *metav1.Time `json:"lastCompletedScanTime,omitempty"`

	// Conditions contains at most Ready and InventoryReady.
	// +optional
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	Summary AuditSummary `json:"summary,omitzero"`

	// Findings contains the deterministic highest-priority bounded result set.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=id
	Findings []OwnershipFinding `json:"findings,omitempty"`

	// TruncatedFindings reports current findings omitted by the 100-item status budget.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TruncatedFindings int32 `json:"truncatedFindings,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Findings",type="integer",JSONPath=".status.summary.totalFindings"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// OwnershipAudit is the Schema for the ownershipaudits API.
type OwnershipAudit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OwnershipAuditSpec `json:"spec"`

	// +optional
	Status OwnershipAuditStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OwnershipAuditList contains a list of OwnershipAudit.
type OwnershipAuditList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OwnershipAudit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OwnershipAudit{}, &OwnershipAuditList{})
}
