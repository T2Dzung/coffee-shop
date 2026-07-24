package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

type mockRESTMapper struct {
	meta.RESTMapper
	mappingFunc  func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error)
	resetCalled  bool
	mappingCalls int
}

func (m *mockRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	m.mappingCalls++
	if m.mappingFunc != nil {
		return m.mappingFunc(gk, versions...)
	}
	return nil, &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{Group: gk.Group, Resource: "applications"}}
}

func (m *mockRESTMapper) Reset() {
	m.resetCalled = true
}

type mockDiscovery struct {
	*discoveryFakeStub
}

type discoveryFakeStub struct {
	resourcesFunc func(gv string) (*metav1.APIResourceList, error)
}

type forbiddenOwnerReader struct {
	client.Reader
	ownerName string
}

func (r forbiddenOwnerReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == r.ownerName && obj.GetObjectKind().GroupVersionKind().Kind == "Deployment" {
		return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, key.Name, errors.New("forbidden"))
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (m mockDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	if m.resourcesFunc != nil {
		return m.resourcesFunc(gv)
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{}, gv)
}

func TestParserExtractsArgoApplicationDefensively(t *testing.T) {
	parser := inventory.NewSafeParser()

	// 1. Valid application fixture
	validApp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      "test-app",
				"namespace": "argocd",
				"uid":       "app-uid-123",
			},
			"spec": map[string]interface{}{
				"syncPolicy": map[string]interface{}{
					"automated": map[string]interface{}{
						"prune": true,
					},
				},
			},
			"status": map[string]interface{}{
				"sync": map[string]interface{}{
					"status": "Synced",
				},
				"resources": []interface{}{
					map[string]interface{}{
						"group":     "apps",
						"version":   "v1",
						"kind":      "ReplicaSet",
						"namespace": "coffeeshop",
						"name":      "coffeeshop-rabbitmq-12345",
					},
				},
			},
		},
	}

	evidence, err := parser.ParseApplication(validApp)
	if err != nil {
		t.Fatalf("ParseApplication failed: %v", err)
	}

	if evidence.SyncStatus != "Synced" {
		t.Errorf("expected SyncStatus to be Synced, got %s", evidence.SyncStatus)
	}
	if !evidence.AutoPruneEnabled {
		t.Error("expected AutoPruneEnabled to be true")
	}
	if len(evidence.Resources) != 1 {
		t.Fatalf("expected 1 managed resource, got %d", len(evidence.Resources))
	}
	res := evidence.Resources[0].Identity
	if res.Kind != "ReplicaSet" || res.Name != "coffeeshop-rabbitmq-12345" {
		t.Errorf("parsed resource mismatch: %+v", res)
	}

	// 2. Invalid automated prune type (malformed)
	invalidApp := validApp.DeepCopy()
	_ = unstructured.SetNestedField(invalidApp.Object, "not-a-bool", "spec", "syncPolicy", "automated", "prune")
	_, err = parser.ParseApplication(invalidApp)
	if err == nil || !strings.Contains(err.Error(), "automated.prune is not a boolean") {
		t.Errorf("expected error containing 'automated.prune is not a boolean', got: %v", err)
	}
}

func TestParserExtractsProtectionAnnotations(t *testing.T) {
	parser := inventory.NewSafeParser()

	// 1. Target with compare-options IgnoreExtraneous
	rs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "ReplicaSet",
			"metadata": map[string]interface{}{
				"name":      "rs-1",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"argocd.argoproj.io/compare-options": "IgnoreExtraneous",
				},
			},
		},
	}

	prot := parser.ParseProtection(rs)
	if !prot.IgnoreExtraneous {
		t.Error("expected IgnoreExtraneous to be true due to compare-options")
	}
	if prot.PruneFalse {
		t.Error("expected PruneFalse to be false for compare-options alone")
	}

	// 2. Target with sync-options Prune=false
	rs2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "ReplicaSet",
			"metadata": map[string]interface{}{
				"name":      "rs-2",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"argocd.argoproj.io/sync-options": "Prune=false, Validate=false",
				},
			},
		},
	}
	prot2 := parser.ParseProtection(rs2)
	if !prot2.PruneFalse {
		t.Error("expected PruneFalse to be true due to sync-options")
	}
	if prot2.IgnoreExtraneous {
		t.Error("expected IgnoreExtraneous to be false for sync-options alone")
	}
}

func TestCollectorUnsupportedTargetGVK(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeReader := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeDyn := dynamicfake.NewSimpleDynamicClient(scheme)
	discHelper := inventory.NewDiscoveryHelper(mockDiscovery{}, &mockRESTMapper{})
	collector := inventory.NewCollector(fakeReader, fakeDyn, discHelper)

	spec := &guardplatformv1alpha1.OwnershipAuditSpec{
		TargetRules: []guardplatformv1alpha1.TargetRule{
			{APIGroup: "", Version: "v1", Kind: "Pod"}, // Unsupported GVK at runtime
		},
	}

	_, err := collector.Collect(context.Background(), "default", spec)
	invErr, ok := err.(*inventory.InventoryError)
	if !ok || invErr.DTO.Class != inventory.ErrInvalidInventoryScope {
		t.Errorf("expected typed InvalidInventoryScope error, got: %#v", err)
	}
}

func TestCollectorArgoCDCRDAbsent(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeReader := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeDyn := dynamicfake.NewSimpleDynamicClient(scheme)

	// Mock RESTMapper to return NoResourceMatchError (CRD absent)
	mapper := &mockRESTMapper{
		mappingFunc: func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
			return nil, &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{Group: gk.Group, Resource: "applications"}}
		},
	}
	discHelper := inventory.NewDiscoveryHelper(mockDiscovery{}, mapper)
	collector := inventory.NewCollector(fakeReader, fakeDyn, discHelper)

	spec := &guardplatformv1alpha1.OwnershipAuditSpec{
		ApplicationRefs: []guardplatformv1alpha1.ApplicationReference{
			{Namespace: "argocd", Name: "app-1"},
		},
		TargetRules: []guardplatformv1alpha1.TargetRule{
			{APIGroup: "apps", Version: "v1", Kind: "Deployment"},
		},
	}

	snapshot, err := collector.Collect(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if snapshot.ArgoDiscoveryState != inventory.DiscoveryUnavailable {
		t.Errorf("expected discovery state to be Unavailable, got %s", snapshot.ArgoDiscoveryState)
	}
	if snapshot.ArgoDiscoveryError == nil || snapshot.ArgoDiscoveryError.Class != inventory.ErrDependencyUnavailable {
		t.Errorf("expected DependencyUnavailable error, got: %v", snapshot.ArgoDiscoveryError)
	}
	if !mapper.resetCalled {
		t.Error("expected RESTMapper Reset to be called")
	}
}

func TestCollectorArgoCDForbidden(t *testing.T) {
	scheme := runtime.NewScheme()
	fakeReader := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeDyn := dynamicfake.NewSimpleDynamicClient(scheme)

	// Mock RESTMapper to allow mapping but Discovery to return 403 Forbidden
	mapper := &mockRESTMapper{
		mappingFunc: func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
			return &meta.RESTMapping{
				Resource:         schema.GroupVersionResource{Group: gk.Group, Version: "v1alpha1", Resource: "applications"},
				GroupVersionKind: gk.WithVersion("v1alpha1"),
			}, nil
		},
	}
	disc := mockDiscovery{
		discoveryFakeStub: &discoveryFakeStub{
			resourcesFunc: func(gv string) (*metav1.APIResourceList, error) {
				return nil, apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io"}, "", errors.New("forbidden"))
			},
		},
	}

	discHelper := inventory.NewDiscoveryHelper(disc, mapper)
	collector := inventory.NewCollector(fakeReader, fakeDyn, discHelper)

	spec := &guardplatformv1alpha1.OwnershipAuditSpec{
		TargetRules: []guardplatformv1alpha1.TargetRule{
			{APIGroup: "apps", Version: "v1", Kind: "Deployment"},
		},
	}

	snapshot, err := collector.Collect(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if snapshot.ArgoDiscoveryState != inventory.DiscoveryForbidden {
		t.Errorf("expected discovery state to be Forbidden, got %s", snapshot.ArgoDiscoveryState)
	}
	if snapshot.ArgoDiscoveryError == nil || snapshot.ArgoDiscoveryError.Class != inventory.ErrEvidenceForbidden {
		t.Errorf("expected ErrEvidenceForbidden, got: %v", snapshot.ArgoDiscoveryError)
	}
}

func TestCollectorCollectsNormalizedEvidence(t *testing.T) {
	scheme := runtime.NewScheme()

	// 1. Create a live deployment and ReplicaSet in fake Reader (cache)
	depGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	dep := &unstructured.Unstructured{}
	dep.SetGroupVersionKind(depGVK)
	dep.SetName("app-deploy")
	dep.SetNamespace("coffeeshop")
	dep.SetUID("deploy-uid-456")

	rsGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	rs := &unstructured.Unstructured{}
	rs.SetGroupVersionKind(rsGVK)
	rs.SetName("app-rs")
	rs.SetNamespace("coffeeshop")
	rs.SetUID("rs-uid-789")
	rs.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "app-deploy",
			UID:        "deploy-uid-456",
		},
		{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "non-existent-deploy",
			UID:        "non-existent-uid",
		},
	})
	rs.SetAnnotations(map[string]string{
		"argocd.argoproj.io/compare-options": "IgnoreExtraneous",
	})

	fakeReader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(dep, rs).Build()

	// 2. Mock dynamic client to return Argo Application
	appGVK := schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(appGVK)
	app.SetName("coffeeshop-rabbitmq")
	app.SetNamespace("argocd")
	app.SetUID("app-uid-111")
	_ = unstructured.SetNestedField(app.Object, "Synced", "status", "sync", "status")
	_ = unstructured.SetNestedSlice(app.Object, []interface{}{
		map[string]interface{}{
			"group":     "apps",
			"kind":      "ReplicaSet",
			"namespace": "coffeeshop",
			"name":      "app-rs",
		},
	}, "status", "resources")

	fakeDyn := dynamicfake.NewSimpleDynamicClient(scheme, app)

	// 3. Mock discovery
	mapper := &mockRESTMapper{
		mappingFunc: func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
			return &meta.RESTMapping{
				Resource:         schema.GroupVersionResource{Group: gk.Group, Version: "v1alpha1", Resource: "applications"},
				GroupVersionKind: gk.WithVersion("v1alpha1"),
			}, nil
		},
	}
	disc := mockDiscovery{
		discoveryFakeStub: &discoveryFakeStub{
			resourcesFunc: func(gv string) (*metav1.APIResourceList, error) {
				return &metav1.APIResourceList{
					APIResources: []metav1.APIResource{{Kind: "Application"}},
				}, nil
			},
		},
	}

	discHelper := inventory.NewDiscoveryHelper(disc, mapper)
	collector := inventory.NewCollector(fakeReader, fakeDyn, discHelper)

	spec := &guardplatformv1alpha1.OwnershipAuditSpec{
		ApplicationRefs: []guardplatformv1alpha1.ApplicationReference{
			{Namespace: "argocd", Name: "coffeeshop-rabbitmq"},
		},
		TargetRules: []guardplatformv1alpha1.TargetRule{
			{APIGroup: "apps", Version: "v1", Kind: "ReplicaSet"},
		},
	}

	snapshot, err := collector.Collect(context.Background(), "coffeeshop", spec)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if snapshot.ArgoDiscoveryState != inventory.DiscoveryAvailable {
		t.Errorf("expected DiscoveryAvailable, got %s", snapshot.ArgoDiscoveryState)
	}

	// Verify Application Evidence
	if len(snapshot.Applications) != 1 {
		t.Fatalf("expected 1 Application evidence, got %d", len(snapshot.Applications))
	}
	appEvidence := snapshot.Applications[0]
	if appEvidence.SyncStatus != "Synced" {
		t.Errorf("expected Synced, got %s", appEvidence.SyncStatus)
	}

	// Verify Protections Evidence
	if len(snapshot.Protections) != 1 {
		t.Fatalf("expected 1 protection, got %d", len(snapshot.Protections))
	}
	prot := snapshot.Protections[0]
	if !prot.IgnoreExtraneous {
		t.Error("expected IgnoreExtraneous to be true due to rs annotation")
	}
	if prot.PruneFalse {
		t.Error("expected PruneFalse to be false for compare-options alone")
	}

	// Verify Owners Evidence (2 owner references)
	if len(snapshot.Owners) != 2 {
		t.Fatalf("expected 2 owner evidences, got %d", len(snapshot.Owners))
	}

	// Owner 1: Resolved existing deploy
	o1 := snapshot.Owners[0]
	if o1.OwnerName != "app-deploy" || o1.LookupResult != inventory.OwnerResolved {
		t.Errorf("expected app-deploy to be Resolved, got name=%s, result=%s", o1.OwnerName, o1.LookupResult)
	}
	if o1.ObservedOwnerUID != "deploy-uid-456" {
		t.Errorf("owner UID mismatch: expected deploy-uid-456, got %s", o1.ObservedOwnerUID)
	}

	// Owner 2: a cache miss is not authoritative deletion evidence.
	o2 := snapshot.Owners[1]
	if o2.OwnerName != "non-existent-deploy" || o2.LookupResult != inventory.OwnerUnknown {
		t.Errorf("expected non-existent-deploy cache miss to be Unknown, got name=%s, result=%s", o2.OwnerName, o2.LookupResult)
	}
	if o2.Metadata.SourceError == nil || o2.Metadata.SourceError.Class != inventory.ErrStaleEvidence {
		t.Errorf("expected cache miss to carry StaleEvidence, got %#v", o2.Metadata.SourceError)
	}
}

func TestCollectorClassifiesForbiddenOwnerLookup(t *testing.T) {
	scheme := runtime.NewScheme()
	rs := &unstructured.Unstructured{}
	rs.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"})
	rs.SetName("app-rs")
	rs.SetNamespace("coffeeshop")
	rs.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "restricted-owner",
		UID:        "restricted-owner-uid",
	}})

	baseReader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rs).Build()
	reader := forbiddenOwnerReader{Reader: baseReader, ownerName: "restricted-owner"}
	mapper, disc := availableDiscovery()
	collector := inventory.NewCollector(reader, dynamicfake.NewSimpleDynamicClient(scheme), inventory.NewDiscoveryHelper(disc, mapper))

	snapshot, err := collector.Collect(context.Background(), "coffeeshop", &guardplatformv1alpha1.OwnershipAuditSpec{
		TargetRules: []guardplatformv1alpha1.TargetRule{{APIGroup: "apps", Version: "v1", Kind: "ReplicaSet"}},
	})
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}
	if len(snapshot.Owners) != 1 {
		t.Fatalf("expected one owner evidence, got %d", len(snapshot.Owners))
	}
	owner := snapshot.Owners[0]
	if owner.LookupResult != inventory.OwnerForbidden {
		t.Fatalf("expected forbidden lookup result, got %s", owner.LookupResult)
	}
	if owner.Metadata.Freshness != inventory.FreshnessUnknown || owner.Metadata.SourceError == nil || owner.Metadata.SourceError.Class != inventory.ErrEvidenceForbidden {
		t.Fatalf("forbidden owner must remain unhealthy EvidenceForbidden, got metadata=%#v", owner.Metadata)
	}
}

func availableDiscovery() (*mockRESTMapper, mockDiscovery) {
	mapper := &mockRESTMapper{mappingFunc: func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
		return &meta.RESTMapping{Resource: schema.GroupVersionResource{Group: gk.Group, Version: "v1alpha1", Resource: "applications"}, GroupVersionKind: gk.WithVersion("v1alpha1")}, nil
	}}
	disc := mockDiscovery{discoveryFakeStub: &discoveryFakeStub{resourcesFunc: func(string) (*metav1.APIResourceList, error) {
		return &metav1.APIResourceList{APIResources: []metav1.APIResource{{Kind: "Application"}}}, nil
	}}}
	return mapper, disc
}

func TestDiscoveryResetRetriesOnceAndRecovers(t *testing.T) {
	mapper := &mockRESTMapper{}
	mapper.mappingFunc = func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
		if mapper.mappingCalls == 1 {
			return nil, &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{Group: gk.Group, Resource: "applications"}}
		}
		return &meta.RESTMapping{Resource: schema.GroupVersionResource{Group: gk.Group, Version: "v1alpha1", Resource: "applications"}, GroupVersionKind: gk.WithVersion("v1alpha1")}, nil
	}
	_, disc := availableDiscovery()
	state, errDTO := inventory.NewDiscoveryHelper(disc, mapper).IsArgoInstalled(context.Background())
	if state != inventory.DiscoveryAvailable || errDTO != nil || !mapper.resetCalled || mapper.mappingCalls != 2 {
		t.Fatalf("expected bounded reset recovery, state=%s error=%#v reset=%t calls=%d", state, errDTO, mapper.resetCalled, mapper.mappingCalls)
	}
}

func TestParserDistinguishesMissingOptionalFromWrongType(t *testing.T) {
	parser := inventory.NewSafeParser()
	missing := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]interface{}{"name": "missing", "namespace": "argocd"},
	}}
	evidence, err := parser.ParseApplication(missing)
	if err != nil || evidence.SyncStatusKnown {
		t.Fatalf("missing optional sync status must be valid and unknown: %#v %v", evidence, err)
	}
	wrong := missing.DeepCopy()
	_ = unstructured.SetNestedField(wrong.Object, int64(42), "status", "sync", "status")
	if _, err := parser.ParseApplication(wrong); err == nil {
		t.Fatal("wrong-type optional sync status must be malformed")
	}
}

func TestCollectorBoundsCallsRejectsSecretAndMarksStale(t *testing.T) {
	scheme := runtime.NewScheme()
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	mapper, disc := availableDiscovery()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	collector := inventory.NewCollector(reader, dyn, inventory.NewDiscoveryHelper(disc, mapper))
	collector.Now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	refs := make([]guardplatformv1alpha1.ApplicationReference, 21)
	for i := range refs {
		refs[i] = guardplatformv1alpha1.ApplicationReference{Namespace: "argocd", Name: fmt.Sprintf("app-%02d", i)}
	}
	spec := &guardplatformv1alpha1.OwnershipAuditSpec{ApplicationRefs: refs, TargetRules: []guardplatformv1alpha1.TargetRule{{APIGroup: "apps", Version: "v1", Kind: "Deployment"}}, ResyncInterval: metav1.Duration{Duration: 10 * time.Minute}}
	if _, err := collector.Collect(context.Background(), "default", spec); err != nil {
		t.Fatalf("not-found refs are normalized domain evidence: %v", err)
	}
	if got := len(dyn.Actions()); got != 20 {
		t.Fatalf("expected 20 bounded dynamic calls, got %d", got)
	}
	secretSpec := &guardplatformv1alpha1.OwnershipAuditSpec{TargetRules: []guardplatformv1alpha1.TargetRule{{APIGroup: "", Version: "v1", Kind: "Secret"}}}
	before := len(dyn.Actions())
	_, err := collector.Collect(context.Background(), "default", secretSpec)
	invErr, ok := err.(*inventory.InventoryError)
	if !ok || invErr.DTO.Class != inventory.ErrInvalidInventoryScope || len(dyn.Actions()) != before {
		t.Fatalf("Secret must be rejected before API read: %#v", err)
	}
}

func TestCollectorNormalizesStaleConditionsWithoutRawData(t *testing.T) {
	scheme := runtime.NewScheme()
	app := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]interface{}{"name": "stale-app", "namespace": "argocd", "resourceVersion": "77"},
		"spec":     map[string]interface{}{"source": map[string]interface{}{"password": "must-not-leak"}},
		"status": map[string]interface{}{
			"reconciledAt": "2026-07-19T11:30:00Z",
			"conditions":   []interface{}{map[string]interface{}{"type": "ComparisonError", "message": strings.Repeat("x", 700)}},
		},
	}}
	app.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
	mapper, disc := availableDiscovery()
	collector := inventory.NewCollector(fake.NewClientBuilder().WithScheme(scheme).Build(), dynamicfake.NewSimpleDynamicClient(scheme, app), inventory.NewDiscoveryHelper(disc, mapper))
	collector.Now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	spec := &guardplatformv1alpha1.OwnershipAuditSpec{
		ApplicationRefs: []guardplatformv1alpha1.ApplicationReference{{Namespace: "argocd", Name: "stale-app"}},
		TargetRules:     []guardplatformv1alpha1.TargetRule{{APIGroup: "apps", Version: "v1", Kind: "Deployment"}},
		ResyncInterval:  metav1.Duration{Duration: 10 * time.Minute},
	}
	snapshot, err := collector.Collect(context.Background(), "default", spec)
	if err != nil {
		t.Fatalf("collect stale fixture: %v", err)
	}
	if len(snapshot.Applications) != 1 || snapshot.Applications[0].Metadata.Freshness != inventory.FreshnessStale {
		t.Fatalf("expected stale application evidence: %#v", snapshot.Applications)
	}
	if got := len(snapshot.Applications[0].SourceConditions[0].Message); got != 512 {
		t.Fatalf("expected bounded condition message, got %d", got)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("normalized snapshot leaked raw data: %s %v", encoded, err)
	}
}

func TestDiscoveryTransientIsUnknownAndReturned(t *testing.T) {
	mapper, _ := availableDiscovery()
	disc := mockDiscovery{discoveryFakeStub: &discoveryFakeStub{resourcesFunc: func(string) (*metav1.APIResourceList, error) {
		return nil, apierrors.NewTimeoutError("temporary discovery failure", 1)
	}}}
	collector := inventory.NewCollector(fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), inventory.NewDiscoveryHelper(disc, mapper))
	spec := &guardplatformv1alpha1.OwnershipAuditSpec{TargetRules: []guardplatformv1alpha1.TargetRule{{APIGroup: "apps", Version: "v1", Kind: "Deployment"}}}
	snapshot, err := collector.Collect(context.Background(), "default", spec)
	invErr, ok := err.(*inventory.InventoryError)
	if !ok || invErr.DTO.Class != inventory.ErrTransientReadFailure || snapshot.ArgoDiscoveryState != inventory.DiscoveryUnknown {
		t.Fatalf("transient discovery must remain Unknown and retryable: state=%s error=%#v", snapshot.ArgoDiscoveryState, err)
	}
}

func TestParserRejectsWrongTypeInManagedResource(t *testing.T) {
	app := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]interface{}{"name": "wrong-resource", "namespace": "argocd"},
		"status":   map[string]interface{}{"resources": []interface{}{map[string]interface{}{"kind": int64(7), "name": "target"}}},
	}}
	if _, err := inventory.NewSafeParser().ParseApplication(app); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("wrong-type managed resource field must be malformed, got %v", err)
	}
}

func FuzzSafeParserNeverPanics(f *testing.F) {
	f.Add([]byte(`{"apiVersion":"argoproj.io/v1alpha1","kind":"Application","metadata":{"name":"app","namespace":"argocd"}}`))
	f.Add([]byte(`{"status":{"resources":[{"kind":7,"name":"target"}]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var object map[string]interface{}
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return
		}
		_, _ = inventory.NewSafeParser().ParseApplication(&unstructured.Unstructured{Object: object})
	})
}
