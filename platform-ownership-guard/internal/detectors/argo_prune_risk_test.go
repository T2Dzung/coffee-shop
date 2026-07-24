package detectors_test

import (
	"testing"
	"time"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/detectors"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

func TestArgoPruneRiskRabbitMQIncidentReplay(t *testing.T) {
	// Replay RabbitMQ incident: Secret generated outside Git, Argo reports requiresPruning=true, autoPrune=true
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "rabbitmq-app",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				SyncStatus:       "Synced",
				SyncStatusKnown:  true,
				AutoPruneKnown:   true,
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "", Version: "v1", Kind: "Secret",
							Namespace: "coffeeshop", Name: "rabbitmq-default-user",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		// Secret protection is unreadable by design in Guard
		Protections: nil,
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for RabbitMQ incident replay, got %d", len(findings))
	}

	f := findings[0]
	if f.Detector != guardplatformv1alpha1.DetectorArgoPruneRisk {
		t.Errorf("expected detector ArgoPruneRisk, got %s", f.Detector)
	}
	if f.Confidence != guardplatformv1alpha1.ConfidenceSuspected {
		t.Errorf("expected confidence Suspected for Secret, got %s", f.Confidence)
	}
	if f.Severity != guardplatformv1alpha1.SeverityHigh {
		t.Errorf("expected severity High for Secret prune risk, got %s", f.Severity)
	}
	if f.Target.Kind != "Secret" || f.Target.Name != "rabbitmq-default-user" {
		t.Errorf("target mismatch: %+v", f.Target)
	}
}

func TestArgoPruneRiskCleanFixture(t *testing.T) {
	// Target resource with explicit Prune=false protection
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "order-app",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				AutoPruneKnown:   true,
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "coffeeshop", Name: "order-service",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		Protections: []inventory.ProtectionEvidence{
			{
				TargetRef: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "coffeeshop", Name: "order-service",
				},
				Readable:              true,
				IgnoreExtraneousKnown: true,
				IgnoreExtraneous:      false,
				PruneFalseKnown:       true,
				PruneFalse:            true, // Protected!
			},
		},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for protected resource, got %d", len(findings))
	}
}

func TestArgoPruneRiskIgnoreExtraneousDoesNotSuppressRisk(t *testing.T) {
	// Resource has CompareOptions=IgnoreExtraneous, but NOT Prune=false
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "payment-app",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				AutoPruneKnown:   true,
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "coffeeshop", Name: "payment-service",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		Protections: []inventory.ProtectionEvidence{
			{
				TargetRef: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "coffeeshop", Name: "payment-service",
				},
				Readable:              true,
				IgnoreExtraneousKnown: true,
				IgnoreExtraneous:      true, // IgnoreExtraneous present
				PruneFalseKnown:       true,
				PruneFalse:            false, // Prune=false NOT present!
			},
		},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding because IgnoreExtraneous does not protect prune, got %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != guardplatformv1alpha1.ConfidenceConfirmed {
		t.Errorf("expected Confirmed for readable Deployment with auto-prune, got %s", f.Confidence)
	}
}

func TestArgoPruneRiskMissingOrUnreadableProtectionIsSuspected(t *testing.T) {
	now := time.Now()
	// Target readable is false
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "app-1",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "coffeeshop", Name: "dep-unreadable",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		Protections: []inventory.ProtectionEvidence{
			{
				TargetRef: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "coffeeshop", Name: "dep-unreadable",
				},
				Readable: false, // Unreadable protection!
			},
		},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Confidence != guardplatformv1alpha1.ConfidenceSuspected {
		t.Errorf("expected Suspected confidence when protection is unreadable, got %s", findings[0].Confidence)
	}
}

func TestArgoPruneRiskUnknownProtectionAndAutoPruneNeverConfirm(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		TargetNamespace:    "coffeeshop",
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{{
			ApplicationRef:   inventory.ResourceIdentity{APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application", Namespace: "argocd", Name: "unknown-flags"},
			Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
			AutoPruneKnown:   false,
			AutoPruneEnabled: true,
			Resources: []inventory.ApplicationResourceEvidence{{
				Identity:        inventory.ResourceIdentity{APIGroup: "apps", Version: "v1", Kind: "Deployment", Namespace: "coffeeshop", Name: "unknown-flags"},
				RequiresPruning: true,
			}},
		}},
		Protections: []inventory.ProtectionEvidence{{
			TargetRef:       inventory.ResourceIdentity{APIGroup: "apps", Version: "v1", Kind: "Deployment", Namespace: "coffeeshop", Name: "unknown-flags"},
			Readable:        true,
			PruneFalseKnown: false,
		}},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 || findings[0].Confidence != guardplatformv1alpha1.ConfidenceSuspected {
		t.Fatalf("unknown protection/auto-prune evidence must remain Suspected, got %+v", findings)
	}
}

func TestArgoPruneRiskBlockingConditionIsInsufficientEvidence(t *testing.T) {
	now := time.Now()
	// Application contains ComparisonError condition
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "app-err",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				AutoPruneEnabled: true,
				SourceConditions: []inventory.SourceCondition{
					{Type: "ComparisonError", Message: "failed to compare manifests"},
				},
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "apps", Version: "v1", Kind: "Deployment",
							Namespace: "coffeeshop", Name: "dep-err",
						},
						RequiresPruning: true,
					},
				},
			},
		},
		Protections: []inventory.ProtectionEvidence{
			{
				TargetRef: inventory.ResourceIdentity{
					APIGroup: "apps", Version: "v1", Kind: "Deployment",
					Namespace: "coffeeshop", Name: "dep-err",
				},
				Readable:   true,
				PruneFalse: false,
			},
		},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Confidence != guardplatformv1alpha1.ConfidenceInsufficientEvidence {
		t.Errorf("expected InsufficientEvidence for blocking Application condition, got %s", findings[0].Confidence)
	}
}

func TestArgoPruneRiskUnknownFreshnessCannotClaimProtectedClean(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		TargetNamespace:    "coffeeshop",
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{{
			ApplicationRef: inventory.ResourceIdentity{APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application", Namespace: "argocd", Name: "unknown-app"},
			Metadata:       inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessUnknown},
			Resources: []inventory.ApplicationResourceEvidence{{
				Identity:        inventory.ResourceIdentity{APIGroup: "apps", Version: "v1", Kind: "Deployment", Namespace: "coffeeshop", Name: "protected-deployment"},
				RequiresPruning: true,
			}},
		}},
		Protections: []inventory.ProtectionEvidence{{
			TargetRef:  inventory.ResourceIdentity{APIGroup: "apps", Version: "v1", Kind: "Deployment", Namespace: "coffeeshop", Name: "protected-deployment"},
			Readable:   true,
			PruneFalse: true,
		}},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 1 || findings[0].Confidence != guardplatformv1alpha1.ConfidenceInsufficientEvidence {
		t.Fatalf("unknown Application freshness must produce one InsufficientEvidence finding, got %+v", findings)
	}
}

func TestArgoPruneRiskSkipsResourcesOutsideNamespacedAuditScope(t *testing.T) {
	now := time.Now()
	snapshot := &inventory.NormalizedSnapshot{
		ObservedAt:         now,
		TargetNamespace:    "coffeeshop",
		ArgoDiscoveryState: inventory.DiscoveryAvailable,
		Applications: []inventory.ApplicationEvidence{
			{
				ApplicationRef: inventory.ResourceIdentity{
					APIGroup: "argoproj.io", Version: "v1alpha1", Kind: "Application",
					Namespace: "argocd", Name: "cluster-app",
				},
				Metadata:         inventory.ObservationMetadata{ObservedAt: now, Freshness: inventory.FreshnessFresh},
				AutoPruneEnabled: true,
				Resources: []inventory.ApplicationResourceEvidence{
					{
						Identity: inventory.ResourceIdentity{
							APIGroup: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole",
							Namespace: "", // Cluster-scoped resource!
							Name:      "cluster-admin-custom",
						},
						RequiresPruning: true,
					},
				},
			},
		},
	}

	findings := detectors.EvaluateArgoPruneRisk(snapshot)
	if len(findings) != 0 {
		t.Fatalf("cluster-scoped resource is outside namespaced audit scope; expected no finding, got %+v", findings)
	}
}
