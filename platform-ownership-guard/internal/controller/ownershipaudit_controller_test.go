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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/detectors"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

type mockRESTMapperStub struct {
	meta.RESTMapper
	mappingFunc func(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error)
}

func (m *mockRESTMapperStub) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if m.mappingFunc != nil {
		return m.mappingFunc(gk, versions...)
	}
	return nil, &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{Group: gk.Group, Resource: gk.Kind}}
}

func (m *mockRESTMapperStub) Reset() {}

type mockDiscoveryStub struct{}

func (m mockDiscoveryStub) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	return nil, apierrors.NewNotFound(schema.GroupResource{}, gv)
}

var _ = Describe("OwnershipAudit API and read-only controller", func() {
	ctx := context.Background()

	validAudit := func(name string) *guardplatformv1alpha1.OwnershipAudit {
		return &guardplatformv1alpha1.OwnershipAudit{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: guardplatformv1alpha1.OwnershipAuditSpec{
				ResyncInterval: metav1.Duration{Duration: 10 * time.Minute},
				ApplicationRefs: []guardplatformv1alpha1.ApplicationReference{{
					Namespace: "argocd",
					Name:      "coffeeshop-rabbitmq",
				}},
				Detectors: []guardplatformv1alpha1.DetectorType{
					guardplatformv1alpha1.DetectorArgoPruneRisk,
				},
				TargetRules: []guardplatformv1alpha1.TargetRule{{
					APIGroup: "apps",
					Version:  "v1",
					Kind:     "ReplicaSet",
				}},
			},
		}
	}

	deleteIfPresent := func(name string) {
		resource := &guardplatformv1alpha1.OwnershipAudit{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, resource)
		if err == nil {
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			return
		}
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}

	It("accepts a valid bounded spec and applies the 10m default", func() {
		const name = "valid-defaults"
		defer deleteIfPresent(name)

		audit := validAudit(name)
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, stored)).To(Succeed())
		Expect(stored.Spec.ResyncInterval.Duration).To(Equal(10 * time.Minute))
		Expect(stored.Generation).To(Equal(int64(1)))
	})

	It("accepts stale-only audit without applicationRefs", func() {
		const name = "valid-stale-only"
		defer deleteIfPresent(name)

		audit := validAudit(name)
		audit.Spec.Detectors = []guardplatformv1alpha1.DetectorType{
			guardplatformv1alpha1.DetectorStaleOwnerReference,
		}
		audit.Spec.ApplicationRefs = nil
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, stored)).To(Succeed())
		Expect(stored.Spec.ApplicationRefs).To(BeEmpty())
	})

	DescribeTable("rejects invalid specs at admission",
		func(name string, mutate func(*guardplatformv1alpha1.OwnershipAudit)) {
			audit := validAudit(name)
			mutate(audit)
			Expect(k8sClient.Create(ctx, audit)).NotTo(Succeed())
		},
		Entry("unknown detector", "invalid-detector", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.Detectors = []guardplatformv1alpha1.DetectorType{"UnknownDetector"}
		}),
		Entry("resync below five minutes", "invalid-resync", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.ResyncInterval = metav1.Duration{Duration: 2 * time.Minute}
		}),
		Entry("wildcard target", "invalid-wildcard", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.TargetRules[0].Kind = "*"
		}),
		Entry("empty application refs", "invalid-empty-refs", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.ApplicationRefs = nil
		}),
		Entry("duplicate application refs", "invalid-duplicate-refs", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.ApplicationRefs = append(a.Spec.ApplicationRefs, a.Spec.ApplicationRefs[0])
		}),
		Entry("more than twenty application refs", "invalid-too-many-refs", func(a *guardplatformv1alpha1.OwnershipAudit) {
			a.Spec.ApplicationRefs = make([]guardplatformv1alpha1.ApplicationReference, 21)
			for i := range a.Spec.ApplicationRefs {
				a.Spec.ApplicationRefs[i] = guardplatformv1alpha1.ApplicationReference{
					Namespace: "argocd",
					Name:      fmt.Sprintf("application-%02d", i),
				}
			}
		}),
	)

	It("stores status through the status subresource without changing spec", func() {
		const name = "status-subresource"
		defer deleteIfPresent(name)

		audit := validAudit(name)
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, audit)).To(Succeed())

		audit.Status.ObservedGeneration = audit.Generation
		audit.Status.Conditions = []metav1.Condition{{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "DependencyUnavailable",
			Message:            "Argo Application API is not installed",
			ObservedGeneration: audit.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, audit)).To(Succeed())

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, stored)).To(Succeed())
		Expect(stored.Status.ObservedGeneration).To(Equal(stored.Generation))
		Expect(stored.Status.Conditions).To(HaveLen(1))
		Expect(stored.Spec.ApplicationRefs).To(Equal(audit.Spec.ApplicationRefs))
	})

	It("rejects an oversized condition message on status subresource update", func() {
		const name = "invalid-status-message"
		defer deleteIfPresent(name)

		audit := validAudit(name)
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, audit)).To(Succeed())

		audit.Status.Conditions = []metav1.Condition{{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "MalformedEvidence",
			Message:            strings.Repeat("x", 513),
			ObservedGeneration: audit.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, audit)).NotTo(Succeed())
	})

	It("reconciles audit policy and patches status when Argo CRD is absent", func() {
		const name = "reconcile-crd-absent"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		Expect(k8sClient.Create(ctx, validAudit(name))).To(Succeed())

		fakeDyn := dynamicfake.NewSimpleDynamicClient(k8sClient.Scheme())
		discHelper := inventory.NewDiscoveryHelper(mockDiscoveryStub{}, &mockRESTMapperStub{})
		coll := inventory.NewCollector(k8sClient, fakeDyn, discHelper)

		reconciler := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    coll,
			Jitter:       IdentityJitter,
			Scheme:       k8sClient.Scheme(),
		}

		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(10 * time.Minute))

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		Expect(stored.Status.ObservedGeneration).To(Equal(int64(1)))
		Expect(stored.Status.Conditions).To(HaveLen(2))

		readyCond := meta.FindStatusCondition(stored.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCond.Reason).To(Equal("DependencyUnavailable"))

		inventoryReadyCond := meta.FindStatusCondition(stored.Status.Conditions, "InventoryReady")
		Expect(inventoryReadyCond).NotTo(BeNil())
		Expect(inventoryReadyCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(inventoryReadyCond.Reason).To(Equal("DependencyUnavailable"))
	})

	It("reconciles stale-only audit as Ready without Argo clients", func() {
		const name = "reconcile-stale-only-no-argo"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		audit := validAudit(name)
		audit.Spec.Detectors = []guardplatformv1alpha1.DetectorType{
			guardplatformv1alpha1.DetectorStaleOwnerReference,
		}
		audit.Spec.ApplicationRefs = nil
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		reconciler := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    inventory.NewCollector(k8sClient, nil, nil),
			Evaluator:    detectors.NewEvaluator(),
			Jitter:       IdentityJitter,
			Scheme:       k8sClient.Scheme(),
		}

		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(10 * time.Minute))

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		readyCond := meta.FindStatusCondition(stored.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(readyCond.Reason).To(Equal("InventoryCollected"))
	})

	It("reconciles audit policy with terminal InvalidInventoryScope for unsupported GVK", func() {
		const name = "reconcile-unsupported-gvk"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		audit := validAudit(name)
		audit.Spec.TargetRules = []guardplatformv1alpha1.TargetRule{{
			APIGroup: "",
			Version:  "v1",
			Kind:     "Deployment", // Unsupported group/version combo
		}}
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		fakeDyn := dynamicfake.NewSimpleDynamicClient(k8sClient.Scheme())
		discHelper := inventory.NewDiscoveryHelper(mockDiscoveryStub{}, &mockRESTMapperStub{})
		coll := inventory.NewCollector(k8sClient, fakeDyn, discHelper)

		reconciler := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    coll,
			Jitter:       IdentityJitter,
			Scheme:       k8sClient.Scheme(),
		}

		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(10 * time.Minute))

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		Expect(stored.Status.ObservedGeneration).To(Equal(int64(1)))

		readyCond := meta.FindStatusCondition(stored.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCond.Reason).To(Equal("InvalidInventoryScope"))
	})

	It("proves zero target mutations during full audit reconcile cycle", func() {
		const name = "zero-target-mutation-proof"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		Expect(k8sClient.Create(ctx, validAudit(name))).To(Succeed())

		// Wrap k8sClient in MutationRecorderClient to record all mutating API calls
		recorderClient := NewMutationRecorderClient(k8sClient)

		fakeDyn := dynamicfake.NewSimpleDynamicClient(k8sClient.Scheme())
		discHelper := inventory.NewDiscoveryHelper(mockDiscoveryStub{}, &mockRESTMapperStub{})
		coll := inventory.NewCollector(recorderClient, fakeDyn, discHelper)

		reconciler := &OwnershipAuditReconciler{
			Reader:       recorderClient,
			StatusWriter: recorderClient.Status(),
			Collector:    coll,
			Evaluator:    detectors.NewEvaluator(),
			Scheme:       k8sClient.Scheme(),
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		// Assert zero target mutation calls occurred during reconciliation
		Expect(recorderClient.AssertZeroTargetMutations()).To(Succeed())
	})
	It("keeps an envtest target semantically unchanged", func() {
		const name = "target-unchanged"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)
		Expect(k8sClient.Create(ctx, validAudit(name))).To(Succeed())
		replicas := int32(1)
		target := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: map[string]string{"proof": "unchanged"}}, Spec: appsv1.ReplicaSetSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app:test"}}}}}}
		Expect(k8sClient.Create(ctx, target)).To(Succeed())
		defer func() { Expect(k8sClient.Delete(ctx, target)).To(Succeed()) }()
		before := target.DeepCopy()
		recorder := NewMutationRecorderClient(k8sClient)
		reconciler := &OwnershipAuditReconciler{Reader: recorder, StatusWriter: recorder.Status(), Collector: collectorStub{snapshot: &inventory.NormalizedSnapshot{ArgoDiscoveryState: inventory.DiscoveryAvailable}}, Evaluator: detectors.NewEvaluator()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		after := &appsv1.ReplicaSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(target), after)).To(Succeed())
		Expect(after.Spec).To(Equal(before.Spec))
		Expect(after.Annotations).To(Equal(before.Annotations))
		Expect(after.OwnerReferences).To(Equal(before.OwnerReferences))
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
		Expect(recorder.AssertZeroTargetMutations()).To(Succeed())
	})
	It("builds status findings using real evaluator", func() {
		const name = "real-evaluator-status-findings"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)
		audit := validAudit(name)
		audit.Spec.Detectors = []guardplatformv1alpha1.DetectorType{
			guardplatformv1alpha1.DetectorArgoPruneRisk,
			guardplatformv1alpha1.DetectorStaleOwnerReference,
		}
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		rs := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-rs",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "missing-deploy",
						UID:        "uid-expected-999",
					},
				},
			},
		}

		snapshot := &inventory.NormalizedSnapshot{
			ObservedAt:         time.Now(),
			ArgoDiscoveryState: inventory.DiscoveryAvailable,
			Owners: []inventory.OwnerEvidence{
				{
					DependentIdentity: inventory.ResourceIdentity{
						APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
						Namespace: "default", Name: rs.Name,
					},
					OwnerRefGVK:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					OwnerName:    "missing-deploy",
					OwnerUID:     "uid-expected-999",
					LookupResult: inventory.OwnerNotFound,
				},
			},
		}

		reconciler := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    collectorStub{snapshot: snapshot},
			Evaluator:    detectors.NewEvaluator(),
			Scheme:       k8sClient.Scheme(),
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		stored := &guardplatformv1alpha1.OwnershipAudit{}
		Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
		Expect(stored.Status.Findings).To(HaveLen(1))
		Expect(stored.Status.Findings[0].Detector).To(Equal(guardplatformv1alpha1.DetectorStaleOwnerReference))
		Expect(stored.Status.Findings[0].Confidence).To(Equal(guardplatformv1alpha1.ConfidenceConfirmed))
		Expect(stored.Status.Summary.TotalFindings).To(Equal(int32(1)))
		Expect(stored.Status.Summary.Confirmed).To(Equal(int32(1)))
	})
	It("emits zero duplicate transition events when reconciler restarts with old persisted status", func() {
		const name = "restart-proof-zero-events"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		audit := validAudit(name)
		audit.Spec.Detectors = []guardplatformv1alpha1.DetectorType{
			guardplatformv1alpha1.DetectorStaleOwnerReference,
		}
		Expect(k8sClient.Create(ctx, audit)).To(Succeed())

		snapshot := &inventory.NormalizedSnapshot{
			ObservedAt:         time.Now(),
			ArgoDiscoveryState: inventory.DiscoveryAvailable,
			Owners: []inventory.OwnerEvidence{
				{
					DependentIdentity: inventory.ResourceIdentity{
						APIGroup: "apps", Version: "v1", Kind: "ReplicaSet",
						Namespace: "default", Name: name + "-rs",
					},
					OwnerRefGVK:  schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
					OwnerName:    "missing-deploy",
					OwnerUID:     "uid-999",
					LookupResult: inventory.OwnerNotFound,
				},
			},
		}

		fakeRecorder1 := &FakeEventRecorder{}
		reconciler1 := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    collectorStub{snapshot: snapshot},
			Evaluator:    detectors.NewEvaluator(),
			Recorder:     fakeRecorder1,
			Jitter:       IdentityJitter,
			Scheme:       k8sClient.Scheme(),
		}

		// First reconcile: finding is Added -> Exactly 1 Event emitted
		_, err := reconciler1.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(fakeRecorder1.GetEvents()).To(HaveLen(1))
		Expect(fakeRecorder1.GetEvents()[0].Reason).To(Equal("FindingDetected"))

		// Simulating Manager restart: new reconciler instance created, reads persisted status from k8sClient
		fakeRecorder2 := &FakeEventRecorder{}
		reconciler2 := &OwnershipAuditReconciler{
			Reader:       k8sClient,
			StatusWriter: k8sClient.Status(),
			Collector:    collectorStub{snapshot: snapshot},
			Evaluator:    detectors.NewEvaluator(),
			Recorder:     fakeRecorder2,
			Jitter:       IdentityJitter,
			Scheme:       k8sClient.Scheme(),
		}

		// Second reconcile (after restart): finding is Unchanged -> ZERO events emitted!
		_, err = reconciler2.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(fakeRecorder2.GetEvents()).To(HaveLen(0)) // Zero duplicate events!
	})
})
