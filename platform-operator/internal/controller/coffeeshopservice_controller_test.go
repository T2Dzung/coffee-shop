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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func validService(name string) *platformv1alpha1.CoffeeShopService {
	return &platformv1alpha1.CoffeeShopService{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: platformv1alpha1.CoffeeShopServiceSpec{
			ManagementPolicy: platformv1alpha1.ManagementPolicyObserve,
			Image:            platformv1alpha1.ImageSpec{Repository: "registry.example/web", Tag: "v1.0.0"},
			Replicas:         2,
			Ports:            []platformv1alpha1.ContainerPortSpec{{Name: "http", ContainerPort: 8888}},
			Service: &platformv1alpha1.ServiceSpec{
				Enabled: true,
				Ports:   []platformv1alpha1.ServicePortSpec{{Name: "http", Port: 8888, TargetPort: "http"}},
			},
			Env: []platformv1alpha1.EnvVarSpec{{Name: "REVERSE_PROXY_URL", Value: new("/api")}},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: apiresource.MustParse("50m"), corev1.ResourceMemory: apiresource.MustParse("64Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: apiresource.MustParse("250m"), corev1.ResourceMemory: apiresource.MustParse("128Mi"),
				},
			},
			Probes: &platformv1alpha1.ProbesSpec{
				Readiness: &platformv1alpha1.ProbeSpec{HTTPGet: &platformv1alpha1.HTTPGetAction{Port: "http"}},
			},
			Availability: &platformv1alpha1.AvailabilitySpec{
				PDB: &platformv1alpha1.PDBSpec{Enabled: true, MinAvailable: new(int32(1))},
			},
		},
	}
}

var _ = Describe("CoffeeShopService CRD contract", func() {
	ctx := context.Background()

	AfterEach(func() {
		list := &platformv1alpha1.CoffeeShopServiceList{}
		Expect(k8sClient.List(ctx, list)).To(Succeed())
		for i := range list.Items {
			Expect(k8sClient.Delete(ctx, &list.Items[i])).To(Succeed())
		}
	})

	It("accepts a valid CR and applies static defaults", func() {
		service := validService("valid-defaulting")
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		stored := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.Spec.AdoptionPolicy).To(Equal(platformv1alpha1.AdoptionPolicyNever))
		Expect(stored.Spec.Image.PullPolicy).To(Equal(corev1.PullIfNotPresent))
		Expect(stored.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		Expect(stored.Spec.Service.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		Expect(stored.Spec.Probes.Readiness.HTTPGet.Path).To(Equal("/"))
		Expect(stored.Spec.Probes.Readiness.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
		Expect(stored.Spec.Probes.Readiness.TimeoutSeconds).To(Equal(int32(1)))
		Expect(stored.Spec.Probes.Readiness.PeriodSeconds).To(Equal(int32(10)))
	})

	DescribeTable("accepts versioned sample manifests",
		func(path string) {
			service := readServiceYAML(path)
			Expect(k8sClient.Create(ctx, service)).To(Succeed())
		},
		Entry("web", "config/samples/platform_v1alpha1_coffeeshopservice_web.yaml"),
		Entry("proxy", "config/samples/platform_v1alpha1_coffeeshopservice_proxy.yaml"),
		Entry("worker", "config/samples/platform_v1alpha1_coffeeshopservice_worker.yaml"),
	)

	DescribeTable("rejects invalid manifest fixtures",
		func(path string) {
			service := readServiceYAML(path)
			Expect(k8sClient.Create(ctx, service)).NotTo(Succeed())
		},
		Entry("tag and digest", "test/fixtures/invalid/tag-and-digest.yaml"),
		Entry("env union", "test/fixtures/invalid/env-union.yaml"),
		Entry("single replica PDB", "test/fixtures/invalid/pdb-single-replica.yaml"),
	)

	It("keeps the Phase 6.1 reconciler read-only", func() {
		service := validService("read-only-gate")
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())

		deployment := &appsv1.Deployment{}
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), deployment))).To(BeTrue())
		kubernetesService := &corev1.Service{}
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), kubernetesService))).To(BeTrue())

		stored := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.Status).To(BeZero())
	})

	DescribeTable("rejects invalid cross-field input",
		func(caseName string, mutate func(*platformv1alpha1.CoffeeShopService)) {
			service := validService(fmt.Sprintf("invalid-%s", caseName))
			mutate(service)
			Expect(k8sClient.Create(ctx, service)).NotTo(Succeed())
		},
		Entry("tag-and-digest", "tag-digest", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Image.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}),
		Entry("missing-tag-and-digest", "no-image-id", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Image.Tag = ""
		}),
		Entry("literal-and-valueFrom", "env-union", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Env[0].ValueFrom = &platformv1alpha1.EnvVarSourceSpec{
				ConfigMapKeyRef: &platformv1alpha1.ConfigMapKeySelectorSpec{Name: "coffeeshop-config", Key: "WEB_PORT"},
			}
		}),
		Entry("two-valueFrom-sources", "source-union", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Env[0].Value = nil
			service.Spec.Env[0].ValueFrom = &platformv1alpha1.EnvVarSourceSpec{
				ConfigMapKeyRef: &platformv1alpha1.ConfigMapKeySelectorSpec{Name: "coffeeshop-config", Key: "WEB_PORT"},
				SecretKeyRef:    &platformv1alpha1.SecretKeySelectorSpec{Name: "coffeeshop-secret", Key: "PASSWORD"},
			}
		}),
		Entry("single-replica-pdb", "pdb-replicas", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Replicas = 1
		}),
		Entry("unknown-service-target", "target-port", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Service.Ports[0].TargetPort = "admin"
		}),
		Entry("probe-with-two-handlers", "probe-union", func(service *platformv1alpha1.CoffeeShopService) {
			service.Spec.Probes.Readiness.TCPSocket = &platformv1alpha1.TCPSocketAction{Port: "http"}
		}),
		Entry("missing-memory-limit", "resources", func(service *platformv1alpha1.CoffeeShopService) {
			delete(service.Spec.Resources.Limits, corev1.ResourceMemory)
		}),
	)
})

func clientKey(service *platformv1alpha1.CoffeeShopService) client.ObjectKey {
	return client.ObjectKeyFromObject(service)
}

func readServiceYAML(relativePath string) *platformv1alpha1.CoffeeShopService {
	path := filepath.Join("..", "..", relativePath)
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	service := &platformv1alpha1.CoffeeShopService{}
	Expect(yaml.Unmarshal(data, service)).To(Succeed())
	return service
}
