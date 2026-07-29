package detectors_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/detectors"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

func TestEvaluatorEnabledSubsetSelection(t *testing.T) {
	evaluator := detectors.NewEvaluator()
	now := time.Now()

	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "app-1",
				},
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "default", Name: "dep-1",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		Owners: []inventory.OwnerEvidence{
			{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
					Namespace: "default", Name: "rs-1",
				},
				OwnerRefGVK:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
				OwnerName:        "dep-1",
				OwnerUID:         "uid-1",
				LookupResult:     inventory.OwnerResolved,
				ObservedOwnerUID: "uid-2",
			},
		},
	}

	// 1. Only StaleOwnerReference enabled
	enabledStale := []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorStaleOwnerReference}
	findingsStale := evaluator.Evaluate(snapshot, enabledStale)
	if len(findingsStale) != 1 {
		t.Fatalf("expected 1 finding when only StaleOwnerReference enabled, got %d", len(findingsStale))
	}
	if findingsStale[0].Detector != guardplatformv1alpha1.DetectorStaleOwnerReference {
		t.Errorf("expected detector StaleOwnerReference, got %s", findingsStale[0].Detector)
	}

	// 2. Only ArgoPruneRisk enabled
	enabledPrune := []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorArgoPruneRisk}
	findingsPrune := evaluator.Evaluate(snapshot, enabledPrune)
	if len(findingsPrune) != 1 {
		t.Fatalf("expected 1 finding when only ArgoPruneRisk enabled, got %d", len(findingsPrune))
	}
	if findingsPrune[0].Detector != guardplatformv1alpha1.DetectorArgoPruneRisk {
		t.Errorf("expected detector ArgoPruneRisk, got %s", findingsPrune[0].Detector)
	}

	// 3. Both enabled
	enabledBoth := []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorArgoPruneRisk, guardplatformv1alpha1.DetectorStaleOwnerReference}
	findingsBoth := evaluator.Evaluate(snapshot, enabledBoth)
	if len(findingsBoth) != 2 {
		t.Fatalf("expected 2 findings when both detectors enabled, got %d", len(findingsBoth))
	}
}

func TestEvaluatorDeterminismAndPermutation(t *testing.T) {
	evaluator := detectors.NewEvaluator()
	now := time.Now()

	app1 := inventory.ApplicationEvidence{
		ApplicationRef: inventory.ResourceIdentity{
			APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
			Namespace: "argocd", Name: "app-a",
		},
		AutoPruneEnabled: true,
		Resources: []inventory.ApplicationResourceEvidence{
			{
				Identity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "default", Name: "dep-a",
				},
				RequiresPruning: true,
			},
		},
	}
	app2 := inventory.ApplicationEvidence{
		ApplicationRef: inventory.ResourceIdentity{
			APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
			Namespace: "argocd", Name: "app-b",
		},
		AutoPruneEnabled: true,
		Resources: []inventory.ApplicationResourceEvidence{
			{
				Identity: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "default", Name: "dep-b",
				},
				RequiresPruning: true,
			},
		},
	}

	snapshot1 := &inventory.NormalizedSnapshot{
		ObservedAt:   now,
		Applications: []inventory.ApplicationEvidence{app1, app2},
	}
	snapshot2 := &inventory.NormalizedSnapshot{
		ObservedAt:   now,
		Applications: []inventory.ApplicationEvidence{app2, app1}, // Permuted order
	}

	enabled := []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorArgoPruneRisk}
	findings1 := evaluator.Evaluate(snapshot1, enabled)
	findings2 := evaluator.Evaluate(snapshot2, enabled)

	if len(findings1) != len(findings2) {
		t.Fatalf("finding count mismatch on permuted input: %d vs %d", len(findings1), len(findings2))
	}

	for i := range findings1 {
		if findings1[i].ID != findings2[i].ID {
			t.Errorf("finding[%d] ID mismatch on permuted input: %s vs %s", i, findings1[i].ID, findings2[i].ID)
		}
	}
}

func TestEvaluatorInputImmutability(t *testing.T) {
	evaluator := detectors.NewEvaluator()
	now := time.Now()

	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt: now,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "app-immutability",
				},
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "default", Name: "dep-immutability",
						},
						RequiresPruning: true,
					},
				},
			},
		},
	}

	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	enabled := []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorArgoPruneRisk}
	_ = evaluator.Evaluate(snapshot, enabled)

	after, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("snapshot was mutated during evaluation: before=%s after=%s", before, after)
	}
}

func TestStableFindingIDExcludesTimestampsAndResourceVersions(t *testing.T) {
	target := guardplatformv1alpha1.ResourceReference{
		APIGroup:  "apps",
		Version:   "v1",
		Kind:      "Deployment",
		Namespace: "default",
		Name:      "order-service",
	}

	id1 := detectors.BuildFindingID("v1", guardplatformv1alpha1.DetectorArgoPruneRisk, target, "PruneRisk", "argocd/order-app")
	id2 := detectors.BuildFindingID("v1", guardplatformv1alpha1.DetectorArgoPruneRisk, target, "PruneRisk", "argocd/order-app")
	target.UID = "new-runtime-uid-that-must-not-affect-logical-finding-identity"
	id3 := detectors.BuildFindingID("v1", guardplatformv1alpha1.DetectorArgoPruneRisk, target, "PruneRisk", "argocd/order-app")

	if id1 != id2 || id1 != id3 {
		t.Errorf("stable ID changed across equivalent runtime evidence: %s, %s, %s", id1, id2, id3)
	}

	if len(id1) < 10 || id1[:7] != "sha256:" {
		t.Errorf("expected sha256: prefix in stable ID, got %s", id1)
	}
}

func TestStaleOwnerDetectorUsesSameRuleAcrossResourceFamilies(t *testing.T) {
	evaluator := detectors.NewEvaluator()
	findingFor := func(dependentGroup, dependentKind, ownerGroup, ownerKind string) guardplatformv1alpha1.OwnershipFinding {
		findings := evaluator.Evaluate(&inventory.NormalizedSnapshot{
			ArgoDiscoveryState: inventory.DiscoveryNotRequired,
			Owners: []inventory.OwnerEvidence{{
				DependentIdentity: inventory.ResourceIdentity{
					APIGroup: dependentGroup, Version: "v1", Kind: dependentKind,
					Namespace: "test", Name: "dependent",
				},
				OwnerRefGVK: schema.GroupVersionKind{
					Group: ownerGroup, Version: "v1", Kind: ownerKind,
				},
				OwnerName:        "owner",
				OwnerUID:         "expected-uid",
				LookupResult:     inventory.OwnerResolved,
				ObservedOwnerUID: "replacement-uid",
			}},
		}, []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorStaleOwnerReference})
		if len(findings) != 1 {
			t.Fatalf("expected one finding for %s -> %s, got %d", dependentKind, ownerKind, len(findings))
		}
		return findings[0]
	}

	kubernetesFinding := findingFor("apps", "ReplicaSet", "apps", "Deployment")
	certManagerFinding := findingFor("cert-manager.io", "CertificateRequest", "cert-manager.io", "Certificate")

	if kubernetesFinding.Detector != certManagerFinding.Detector ||
		kubernetesFinding.Confidence != certManagerFinding.Confidence ||
		kubernetesFinding.Severity != certManagerFinding.Severity ||
		kubernetesFinding.Remediation != certManagerFinding.Remediation ||
		!reflect.DeepEqual(kubernetesFinding.MissingEvidence, certManagerFinding.MissingEvidence) {
		t.Fatalf("resource families must share one stale-owner rule: apps=%#v cert-manager=%#v", kubernetesFinding, certManagerFinding)
	}
}
