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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

var _ = Describe("OwnershipAudit API and read-only controller", func() {
	ctx := context.Background()

	validAudit := func(name string) *guardplatformv1alpha1.OwnershipAudit {
		return &guardplatformv1alpha1.OwnershipAudit{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: guardplatformv1alpha1.OwnershipAuditSpec{
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

	It("rejects an oversized condition message on status", func() {
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

	It("reads an existing audit and ignores a deleted audit", func() {
		const name = "read-only-reconcile"
		key := types.NamespacedName{Name: name, Namespace: "default"}
		defer deleteIfPresent(name)

		Expect(k8sClient.Create(ctx, validAudit(name))).To(Succeed())
		reconciler := &OwnershipAuditReconciler{Reader: k8sClient, Scheme: k8sClient.Scheme()}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		deleteIfPresent(name)
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	})
})
