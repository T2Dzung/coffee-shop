package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

type collectorStub struct {
	snapshot *inventory.NormalizedSnapshot
	err      error
}

func (s collectorStub) Collect(context.Context, string, *guardplatformv1alpha1.OwnershipAuditSpec) (*inventory.NormalizedSnapshot, error) {
	return s.snapshot, s.err
}

type countingStatusWriter struct {
	client.SubResourceWriter
	patches int
	err     error
}

func (w *countingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	w.patches++
	if w.err != nil {
		return w.err
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func newReconcileFixture(t *testing.T, name string) (client.Client, *guardplatformv1alpha1.OwnershipAudit) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := guardplatformv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	audit := &guardplatformv1alpha1.OwnershipAudit{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: guardplatformv1alpha1.OwnershipAuditSpec{
			ApplicationRefs: []guardplatformv1alpha1.ApplicationReference{{Namespace: "argocd", Name: "app"}},
			Detectors:       []guardplatformv1alpha1.DetectorType{guardplatformv1alpha1.DetectorArgoPruneRisk},
			TargetRules:     []guardplatformv1alpha1.TargetRule{{APIGroup: "apps", Version: "v1", Kind: "Deployment"}},
			ResyncInterval:  metav1.Duration{Duration: 10 * time.Minute},
		},
	}
	delegate := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&guardplatformv1alpha1.OwnershipAudit{}).WithObjects(audit).Build()
	return delegate, audit
}

func TestReconcileSuppressesSemanticStatusWrites(t *testing.T) {
	delegate, audit := newReconcileFixture(t, "semantic-write")
	writer := &countingStatusWriter{SubResourceWriter: delegate.Status()}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	reconciler := &OwnershipAuditReconciler{Reader: delegate, StatusWriter: writer, Collector: collectorStub{snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable}}, Now: func() time.Time { return now }}
	request := reconcile.Request{NamespacedName: types.NamespacedName{Name: audit.Name, Namespace: audit.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if writer.patches != 1 {
		t.Fatalf("expected one semantic status patch, got %d", writer.patches)
	}
}

func TestReconcilePersistsTransientConditionThenReturnsError(t *testing.T) {
	delegate, audit := newReconcileFixture(t, "transient")
	transient := &inventory.InventoryError{DTO: inventory.ErrorDTO{Class: inventory.ErrTransientReadFailure, Message: "429 overloaded"}}
	reconciler := &OwnershipAuditReconciler{Reader: delegate, StatusWriter: delegate.Status(), Collector: collectorStub{snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryUnknown}, err: transient}}
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: audit.Name, Namespace: audit.Namespace}})
	if !errors.Is(err, transient) {
		t.Fatalf("expected transient error, got %v", err)
	}
	stored := &guardplatformv1alpha1.OwnershipAudit{}
	if err := delegate.Get(context.Background(), client.ObjectKeyFromObject(audit), stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ObservedGeneration != audit.Generation || len(stored.Status.Conditions) != 2 || stored.Status.Conditions[0].Status != metav1.ConditionFalse {
		t.Fatalf("transient status not persisted: %+v", stored.Status)
	}
}

func TestStatusConflictDoesNotOverwriteStaleStatus(t *testing.T) {
	delegate, audit := newReconcileFixture(t, "conflict")
	conflict := apierrors.NewConflict(schema.GroupResource{Group: "guard.platform.t2dzung.github.io", Resource: "ownershipaudits"}, audit.Name, errors.New("conflict"))
	writer := &countingStatusWriter{SubResourceWriter: delegate.Status(), err: conflict}
	reconciler := &OwnershipAuditReconciler{Reader: delegate, StatusWriter: writer, Collector: collectorStub{snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable}}}
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: audit.Name, Namespace: audit.Namespace}})
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	stored := &guardplatformv1alpha1.OwnershipAudit{}
	if err := delegate.Get(context.Background(), client.ObjectKeyFromObject(audit), stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ObservedGeneration != 0 {
		t.Fatalf("stale status was overwritten: %+v", stored.Status)
	}
}

func TestMutationRecorderRejectsEveryNonAuditStatusMutation(t *testing.T) {
	recorder := &MutationRecorderClient{}
	recorder.record("update", &guardplatformv1alpha1.OwnershipAudit{ObjectMeta: metav1.ObjectMeta{Name: "audit"}}, false)
	if recorder.AssertZeroTargetMutations() == nil {
		t.Fatal("root OwnershipAudit mutation must be forbidden")
	}
}
