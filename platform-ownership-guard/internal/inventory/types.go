package inventory

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// ErrorClass defines the categories of errors encountered during data collection.
type ErrorClass string

const (
	ErrDependencyUnavailable ErrorClass = "DependencyUnavailable" // e.g., Argo CRD absent
	ErrEvidenceForbidden     ErrorClass = "EvidenceForbidden"     // RBAC permission issue
	ErrMalformedEvidence     ErrorClass = "MalformedEvidence"     // Schema/type parsing errors
	ErrStaleEvidence         ErrorClass = "StaleEvidence"         // Eventual consistency or sync lag
	ErrTransientReadFailure  ErrorClass = "TransientReadFailure"  // Network timeout, rate limit (429)
	ErrInvalidInventoryScope ErrorClass = "InvalidInventoryScope" // Unsupported target resource GVK
)

// DiscoveryState models the availability of optional dependencies like Argo CD.
type DiscoveryState string

const (
	DiscoveryAvailable   DiscoveryState = "Available"
	DiscoveryUnavailable DiscoveryState = "Unavailable"
	DiscoveryForbidden   DiscoveryState = "Forbidden"
	DiscoveryUnknown     DiscoveryState = "Unknown"
)

// FreshnessState records source freshness without inferring detector results.
type FreshnessState string

const (
	FreshnessFresh   FreshnessState = "Fresh"
	FreshnessStale   FreshnessState = "Stale"
	FreshnessUnknown FreshnessState = "Unknown"
)

// ObservationMetadata captures standard auditing context.
type ObservationMetadata struct {
	ObservedAt            time.Time      `json:"observedAt"`
	SourceObservedAt      *time.Time     `json:"sourceObservedAt,omitempty"`
	SourceResourceVersion string         `json:"sourceResourceVersion"`
	Freshness             FreshnessState `json:"freshness"`
	SourceError           *ErrorDTO      `json:"sourceError,omitempty"`
}

// ErrorDTO wraps classification and sanitized details of a runtime error.
type ErrorDTO struct {
	Class   ErrorClass `json:"class"`
	Message string     `json:"message"`
}

// InventoryError preserves a machine-readable class across package boundaries.
type InventoryError struct {
	DTO ErrorDTO
}

func (e *InventoryError) Error() string { return e.DTO.Message }

func boundedMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

// ResourceIdentity identifies any Kubernetes object inside target rules.
type ResourceIdentity struct {
	APIGroup  string    `json:"apiGroup"`
	Version   string    `json:"version"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	UID       types.UID `json:"uid,omitempty"`
}

// ApplicationResourceEvidence tracks resource identity and whether Argo considers it a prune candidate.
type ApplicationResourceEvidence struct {
	Identity        ResourceIdentity `json:"identity"`
	RequiresPruning bool             `json:"requiresPruning"`
}

// ApplicationEvidence represents normalized state of an Argo CD Application.
type ApplicationEvidence struct {
	ApplicationRef   ResourceIdentity              `json:"applicationRef"`
	Metadata         ObservationMetadata           `json:"metadata"`
	SyncStatus       string                        `json:"syncStatus"`
	SyncStatusKnown  bool                          `json:"syncStatusKnown"`
	SourceConditions []SourceCondition             `json:"sourceConditions,omitempty"`
	AutoPruneKnown   bool                          `json:"autoPruneKnown"`
	AutoPruneEnabled bool                          `json:"autoPruneEnabled"`
	Resources        []ApplicationResourceEvidence `json:"resources,omitempty"`
}

// SourceCondition is the bounded allowlisted subset of an upstream condition.
type SourceCondition struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}

// ProtectionEvidence captures tracking and pruning annotations.
type ProtectionEvidence struct {
	TargetRef             ResourceIdentity    `json:"targetRef"`
	Metadata              ObservationMetadata `json:"metadata"`
	Readable              bool                `json:"readable"`
	IgnoreExtraneousKnown bool                `json:"ignoreExtraneousKnown"`
	IgnoreExtraneous      bool                `json:"ignoreExtraneous"`
	PruneFalseKnown       bool                `json:"pruneFalseKnown"`
	PruneFalse            bool                `json:"pruneFalse"`
}

// OwnerLookupResult defines owner existence state.
type OwnerLookupResult string

const (
	OwnerResolved  OwnerLookupResult = "Resolved"
	OwnerNotFound  OwnerLookupResult = "NotFound"
	OwnerForbidden OwnerLookupResult = "Forbidden"
	OwnerUnknown   OwnerLookupResult = "Unknown"
)

// OwnerEvidence captures a resource's ownerReferences and whether they resolve.
type OwnerEvidence struct {
	DependentIdentity ResourceIdentity        `json:"dependentIdentity"`
	Metadata          ObservationMetadata     `json:"metadata"`
	OwnerRefGVK       schema.GroupVersionKind `json:"ownerRefGVK"`
	OwnerName         string                  `json:"ownerName"`
	OwnerUID          types.UID               `json:"ownerUID"`
	LookupResult      OwnerLookupResult       `json:"lookupResult"`
	ObservedOwnerUID  types.UID               `json:"observedOwnerUID,omitempty"`
}

// NormalizedSnapshot bundles all normalized evidence collected for a Reconcile cycle.
type NormalizedSnapshot struct {
	ObservedAt         time.Time             `json:"observedAt"`
	TargetNamespace    string                `json:"targetNamespace"`
	ArgoDiscoveryState DiscoveryState        `json:"argoDiscoveryState"`
	ArgoDiscoveryError *ErrorDTO             `json:"argoDiscoveryError,omitempty"`
	Applications       []ApplicationEvidence `json:"applications,omitempty"`
	Protections        []ProtectionEvidence  `json:"protections,omitempty"`
	Owners             []OwnerEvidence       `json:"owners,omitempty"`
}
