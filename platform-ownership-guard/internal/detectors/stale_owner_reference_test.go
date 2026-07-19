package detectors_test

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/detectors"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

func TestStaleOwnerReferenceUIDMismatch(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "coffeeshop", Name: "order-service-7f8b9c",
				},
				OwnerRefGVK:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:        "order-service",
				OwnerUID:         "uid-old-111",
				LookupResult:     inventory.OwnerResolved,
				ObservedOwnerUID: "uid-new-222", // UID mismatch!
			},
		},
	}

	findings := detectors.EvaluateStaleOwnerReference(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for UID mismatch, got %d", len(findings))
	}

	f := findings[0]
	if f.Detector != guardplatformv1alpha1.DetectorStaleOwnerReference {
		t.Errorf("expected detector StaleOwnerReference, got %s", f.Detector)
	}
	if f.Confidence != guardplatformv1alpha1.ConfidenceConfirmed {
		t.Errorf("expected confidence Confirmed, got %s", f.Confidence)
	}
	if f.Severity != guardplatformv1alpha1.SeverityWarning {
		t.Errorf("expected severity Warning, got %s", f.Severity)
	}
}

func TestStaleOwnerReferenceEmptyObservedUIDIsInsufficientEvidence(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "coffeeshop", Name: "order-service-empty-uid",
				},
				OwnerRefGVK:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:        "order-service",
				OwnerUID:         "uid-expected-123",
				LookupResult:     inventory.OwnerResolved,
				ObservedOwnerUID: "", // Empty observed UID!
			},
		},
	}

	findings := detectors.EvaluateStaleOwnerReference(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for empty observed UID, got %d", len(findings))
	}
	if findings[0].Confidence != guardplatformv1alpha1.ConfidenceInsufficientEvidence {
		t.Errorf("expected InsufficientEvidence when observed UID is empty, got %s", findings[0].Confidence)
	}
}

func TestStaleOwnerReferenceAuthoritativeNotFound(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "coffeeshop", Name: "payment-service-12345",
				},
				OwnerRefGVK:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:    "payment-service",
				OwnerUID:     "uid-deleted-999",
				LookupResult: inventory.OwnerNotFound, // Authoritative NotFound
			},
		},
	}

	findings := detectors.EvaluateStaleOwnerReference(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for Authoritative NotFound, got %d", len(findings))
	}

	f := findings[0]
	if f.Confidence != guardplatformv1alpha1.ConfidenceConfirmed {
		t.Errorf("expected confidence Confirmed for authoritative NotFound, got %s", f.Confidence)
	}
}

func TestStaleOwnerReferenceCachedMissNeverConfirmed(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "coffeeshop", Name: "inventory-service-abc",
				},
				OwnerRefGVK:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:    "inventory-service",
				OwnerUID:     "uid-unknown-555",
				LookupResult: inventory.OwnerUnknown, // Cached miss / Unknown
			},
		},
	}

	findings := detectors.EvaluateStaleOwnerReference(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for cached miss, got %d", len(findings))
	}

	f := findings[0]
	if f.Confidence != guardplatformv1alpha1.ConfidenceInsufficientEvidence {
		t.Errorf("expected confidence InsufficientEvidence for cached miss, got %s", f.Confidence)
	}
}

func TestStaleOwnerReferenceMatchingOwnerIsClean(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "coffeeshop", Name: "catalog-service-xyz",
				},
				OwnerRefGVK:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:        "catalog-service",
				OwnerUID:         "uid-matching-777",
				LookupResult:     inventory.OwnerResolved,
				ObservedOwnerUID: "uid-matching-777", // Matching!
			},
		},
	}

	findings := detectors.EvaluateStaleOwnerReference(snapshot)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for matching owner, got %d", len(findings))
	}
}
