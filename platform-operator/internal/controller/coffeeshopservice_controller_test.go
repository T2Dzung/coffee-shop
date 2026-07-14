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
	"maps"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/status"
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
		// Clean up child resources (envtest has no GC)
		deployList := &appsv1.DeploymentList{}
		Expect(k8sClient.List(ctx, deployList)).To(Succeed())
		for i := range deployList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &deployList.Items[i]))).To(Succeed())
		}
		svcList := &corev1.ServiceList{}
		Expect(k8sClient.List(ctx, svcList)).To(Succeed())
		for i := range svcList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &svcList.Items[i]))).To(Succeed())
		}
		// Clean up CRs
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

	Context("Observe-only status pipeline (Slice 6.2.1)", func() {
		It("reports missing children without mutating them", func() {
			service := validService("observe-missing")
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify: Child resources are not created
			deployment := &appsv1.Deployment{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), deployment))).To(BeTrue())
			kubernetesService := &corev1.Service{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), kubernetesService))).To(BeTrue())

			// Verify: Status is updated correctly
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
			Expect(stored.Status.ObservedGeneration).To(Equal(service.Generation))
			Expect(stored.Status.DesiredReplicas).To(Equal(service.Spec.Replicas))
			Expect(stored.Status.ReadyReplicas).To(Equal(int32(0)))

			// Kiểm tra conditions
			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			workloadCond := findMetav1Condition(stored.Status.Conditions, status.ConditionWorkloadReady)
			serviceCond := findMetav1Condition(stored.Status.Conditions, status.ConditionGuardrailsReady)

			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("ObserveOnly"))

			Expect(workloadCond).NotTo(BeNil())
			Expect(workloadCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(workloadCond.Reason).To(Equal("WorkloadMissing"))

			Expect(serviceCond).NotTo(BeNil())
			Expect(serviceCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(serviceCond.Reason).To(Equal("ServiceMissing"))
		})

		It("observes existing available resources, updates status, and performs zero writes on secondary reconcile", func() {
			service := validService("observe-existing")
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			// Manually create desired Deployment and Service
			// Build deployment
			desiredDeploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, desiredDeploy)).To(Succeed())

			// Mock deployment status to be ready
			desiredDeploy.Status.Replicas = 2
			desiredDeploy.Status.ReadyReplicas = 2
			desiredDeploy.Status.AvailableReplicas = 2
			Expect(k8sClient.Status().Update(ctx, desiredDeploy)).To(Succeed())

			// Build service
			desiredSvc, err := resource.BuildService(service)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, desiredSvc)).To(Succeed())

			// First reconcile
			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Save resource versions of deployment, service, and parent status
			storedDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedDeploy)).To(Succeed())
			deployRV := storedDeploy.ResourceVersion

			storedSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedSvc)).To(Succeed())
			svcRV := storedSvc.ResourceVersion

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			parentRV := storedParent.ResourceVersion

			// Verify status
			Expect(storedParent.Status.ReadyReplicas).To(Equal(int32(2)))

			workloadCond := findMetav1Condition(storedParent.Status.Conditions, status.ConditionWorkloadReady)
			serviceCond := findMetav1Condition(storedParent.Status.Conditions, status.ConditionGuardrailsReady)
			Expect(workloadCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(workloadCond.Reason).To(Equal("WorkloadAvailable"))
			Expect(serviceCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(serviceCond.Reason).To(Equal("ServiceAvailable"))

			// Second reconcile (no changes)
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify: resourceVersion of child and parent remains unchanged (zero writes)
			Expect(k8sClient.Get(ctx, clientKey(service), storedDeploy)).To(Succeed())
			Expect(storedDeploy.ResourceVersion).To(Equal(deployRV))

			Expect(k8sClient.Get(ctx, clientKey(service), storedSvc)).To(Succeed())
			Expect(storedSvc.ResourceVersion).To(Equal(svcRV))

			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			Expect(storedParent.ResourceVersion).To(Equal(parentRV))
		})

		It("detects drift on child objects but does not mutate them", func() {
			service := validService("observe-drift")
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			// Tạo Deployment bị drift (ví dụ image khác)
			desiredDeploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			// Create drifted Deployment (e.g. different image)
			desiredDeploy.Spec.Template.Spec.Containers[0].Image = "registry.example/drifted:v2.0.0"
			Expect(k8sClient.Create(ctx, desiredDeploy)).To(Succeed())

			// Create drifted Service (e.g. different port)
			desiredSvc, err := resource.BuildService(service)
			Expect(err).NotTo(HaveOccurred())
			desiredSvc.Spec.Ports[0].Port = 9999
			Expect(k8sClient.Create(ctx, desiredSvc)).To(Succeed())

			// Reconcile
			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify: Child resource is not mutated
			storedDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedDeploy)).To(Succeed())
			Expect(storedDeploy.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/drifted:v2.0.0"))

			storedSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedSvc)).To(Succeed())
			Expect(storedSvc.Spec.Ports[0].Port).To(Equal(int32(9999)))

			// Verify: Status reports drift
			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())

			workloadCond := findMetav1Condition(storedParent.Status.Conditions, status.ConditionWorkloadReady)
			serviceCond := findMetav1Condition(storedParent.Status.Conditions, status.ConditionGuardrailsReady)
			Expect(workloadCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(workloadCond.Reason).To(Equal("WorkloadDrifted"))

			Expect(serviceCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(serviceCond.Reason).To(Equal("ServiceDrifted"))
		})
	})

	Context("Manage-mode ownership safety and creation (Slice 6.2.2)", func() {
		It("creates absent Deployment and Service with correct controller ownerReference", func() {
			service := validService("manage-absent")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify Deployment creation and ownerReference
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deploy)).To(Succeed())
			Expect(deploy.OwnerReferences).To(HaveLen(1))
			ownerRef := deploy.OwnerReferences[0]
			Expect(ownerRef.Kind).To(Equal("CoffeeShopService"))
			Expect(ownerRef.Name).To(Equal(service.Name))
			Expect(ownerRef.UID).To(Equal(service.UID))
			Expect(*ownerRef.Controller).To(BeTrue())
			Expect(ownerRef.BlockOwnerDeletion).To(BeNil())

			// Verify Service creation and ownerReference
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), svc)).To(Succeed())
			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].UID).To(Equal(service.UID))

			// Verify status
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())

			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			workloadCond := findMetav1Condition(stored.Status.Conditions, status.ConditionWorkloadReady)
			serviceCond := findMetav1Condition(stored.Status.Conditions, status.ConditionGuardrailsReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(status.ReasonWorkloadUnavailable)) // deployment readyReplicas = 0
			Expect(workloadCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(workloadCond.Reason).To(Equal(status.ReasonWorkloadUnavailable))
			Expect(serviceCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(serviceCond.Reason).To(Equal("ServiceAvailable"))
		})

		It("reconciles children that already have the current parent UID", func() {
			service := validService("manage-owned")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			ownerRef := DesiredOwnerReference(service)
			deploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			deploy.OwnerReferences = []metav1.OwnerReference{ownerRef}
			Expect(k8sClient.Create(ctx, deploy)).To(Succeed())

			svc, err := resource.BuildService(service)
			Expect(err).NotTo(HaveOccurred())
			svc.OwnerReferences = []metav1.OwnerReference{ownerRef}
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			storedDeploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedDeploy)).To(Succeed())
			Expect(storedDeploy.OwnerReferences).To(HaveLen(1))
			Expect(storedDeploy.OwnerReferences[0].UID).To(Equal(service.UID))

			storedSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedSvc)).To(Succeed())
			Expect(storedSvc.OwnerReferences).To(HaveLen(1))
			Expect(storedSvc.OwnerReferences[0].UID).To(Equal(service.UID))

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			ready := findMetav1Condition(storedParent.Status.Conditions, status.ConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Reason).NotTo(Equal(status.ReasonOwnershipConflict))
		})

		It("rejects mutation when there is an unowned Deployment collision", func() {
			service := validService("manage-unowned-collision")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage

			// Create an unowned Deployment beforehand
			unownedDeploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, unownedDeploy)).To(Succeed())
			deployRV := unownedDeploy.ResourceVersion

			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			fakeRecorder := record.NewFakeRecorder(1)
			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient, Recorder: fakeRecorder}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify Deployment remains untouched
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deploy)).To(Succeed())
			Expect(deploy.ResourceVersion).To(Equal(deployRV))
			Expect(deploy.OwnerReferences).To(BeEmpty())

			// Verify Service was NOT created either (cascade block/failsafe)
			svc := &corev1.Service{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), svc))).To(BeTrue())

			// Verify status reports OwnershipConflict
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())

			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			workloadCond := findMetav1Condition(stored.Status.Conditions, status.ConditionWorkloadReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(status.ReasonOwnershipConflict))
			Expect(workloadCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(workloadCond.Reason).To(Equal(status.ReasonOwnershipConflict))
			Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring("Warning OwnershipConflict")))
		})

		It("rejects mutation when there is a foreign controller owner on Deployment", func() {
			service := validService("manage-foreign-collision")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage

			// Create a Deployment owned by another resource (foreign owner)
			foreignDeploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			isController := true
			foreignDeploy.OwnerReferences = []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       "some-replicaset",
					UID:        "12345678-1234-1234-1234-1234567890ab",
					Controller: &isController,
				},
			}
			Expect(k8sClient.Create(ctx, foreignDeploy)).To(Succeed())
			deployRV := foreignDeploy.ResourceVersion

			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify Deployment untouched
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deploy)).To(Succeed())
			Expect(deploy.ResourceVersion).To(Equal(deployRV))

			// Verify status reports OwnershipConflict
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())

			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(status.ReasonOwnershipConflict))
		})

		It("rejects mutation when owner UID is stale", func() {
			service := validService("manage-stale-collision")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage

			// Create a Deployment owned by a previous UID of this CR
			staleDeploy, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			isController := true
			staleDeploy.OwnerReferences = []metav1.OwnerReference{
				{
					APIVersion: platformv1alpha1.GroupVersion.String(),
					Kind:       "CoffeeShopService",
					Name:       service.Name,
					UID:        "stale-uid-12345",
					Controller: &isController,
				},
			}
			Expect(k8sClient.Create(ctx, staleDeploy)).To(Succeed())
			deployRV := staleDeploy.ResourceVersion

			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify Deployment untouched
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deploy)).To(Succeed())
			Expect(deploy.ResourceVersion).To(Equal(deployRV))

			// Verify status reports OwnershipConflict
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())

			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(status.ReasonOwnershipConflict))
		})

		It("applies Deployment successfully but halts on Service collision", func() {
			service := validService("manage-partial-collision")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage

			// Create an unowned Service beforehand
			unownedSvc, err := resource.BuildService(service)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, unownedSvc)).To(Succeed())
			svcRV := unownedSvc.ResourceVersion

			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			// Verify Deployment was created successfully (as it has no collision)
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deploy)).To(Succeed())
			Expect(deploy.OwnerReferences).NotTo(BeEmpty())

			// Verify Service remains untouched
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), svc)).To(Succeed())
			Expect(svc.ResourceVersion).To(Equal(svcRV))

			// Verify status reflects partial conflict
			stored := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())

			readyCond := findMetav1Condition(stored.Status.Conditions, status.ConditionReady)
			workloadCond := findMetav1Condition(stored.Status.Conditions, status.ConditionWorkloadReady)
			serviceCond := findMetav1Condition(stored.Status.Conditions, status.ConditionGuardrailsReady)
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(status.ReasonOwnershipConflict))

			// WorkloadReady should reflect status of Deployment (which is successfully applied, but unavailable)
			Expect(workloadCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(workloadCond.Reason).To(Equal(status.ReasonWorkloadUnavailable))

			// GuardrailsReady should reflect Service collision
			Expect(serviceCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(serviceCond.Reason).To(Equal(status.ReasonOwnershipConflict))
		})
	})

	Context("Manage-mode SSA convergence and cleanup (Slice 6.2.3)", func() {
		It("updates operator-owned fields while preserving external metadata and an injected sidecar", func() {
			service := validService("manage-preserve")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			request := reconcile.Request{NamespacedName: clientKey(service)}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			applyExternalDeploymentFields(service, "test-sidecar-injector", map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{"example.com/owner": "platform-team"},
					"labels":      map[string]any{"example.com/tier": "edge"},
				},
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{map[string]any{
								"name":  "mesh-sidecar",
								"image": "registry.example/mesh:v1",
							}},
						},
					},
				},
			})

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			storedParent.Spec.Image.Tag = "v2.0.0"
			storedParent.Spec.Replicas = 3
			storedParent.Spec.Ports[0].ContainerPort = 8080
			storedParent.Spec.Service.Ports[0].Port = 8080
			Expect(k8sClient.Update(ctx, storedParent)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deployment)).To(Succeed())
			Expect(*deployment.Spec.Replicas).To(Equal(int32(3)))
			Expect(deployment.Annotations).To(HaveKeyWithValue("example.com/owner", "platform-team"))
			Expect(deployment.Labels).To(HaveKeyWithValue("example.com/tier", "edge"))

			var mainContainer, sidecar *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				container := &deployment.Spec.Template.Spec.Containers[i]
				switch container.Name {
				case service.Name:
					mainContainer = container
				case "mesh-sidecar":
					sidecar = container
				}
			}
			Expect(mainContainer).NotTo(BeNil())
			Expect(mainContainer.Image).To(Equal("registry.example/web:v2.0.0"))
			Expect(mainContainer.Ports).To(ContainElement(corev1.ContainerPort{
				Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP,
			}))
			Expect(sidecar).NotTo(BeNil())
			Expect(sidecar.Image).To(Equal("registry.example/mesh:v1"))
		})

		It("repairs drift written by the operator field manager", func() {
			service := validService("manage-self-heal")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			request := reconcile.Request{NamespacedName: clientKey(service)}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			drifted, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			drifted.Spec.Template.Spec.Containers[0].Image = "registry.example/web:drifted"
			Expect(applyObject(ctx, k8sClient, reconciler.Scheme(), service, drifted)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/web:v1.0.0"))
		})

		It("does not force an external image owner and emits one event for a persistent conflict", func() {
			service := validService("manage-apply-conflict")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			fakeRecorder := record.NewFakeRecorder(4)
			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient, Recorder: fakeRecorder}
			request := reconcile.Request{NamespacedName: clientKey(service)}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			applyExternalDeploymentFields(service, "external-image-manager", map[string]any{
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{map[string]any{
								"name":  service.Name,
								"image": "registry.example/web:external",
							}},
						},
					},
				},
			})

			result, firstErr := reconciler.Reconcile(ctx, request)
			Expect(firstErr).To(HaveOccurred())
			Expect(apierrors.IsConflict(firstErr)).To(BeTrue())
			Expect(result).To(Equal(reconcile.Result{}))

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), deployment)).To(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/web:external"))
			Expect(managedFieldManagers(deployment)).To(ContainElements(FieldManager, "external-image-manager"))

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			ready := findMetav1Condition(storedParent.Status.Conditions, status.ConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Reason).To(Equal(status.ReasonApplyConflict))

			_, secondErr := reconciler.Reconcile(ctx, request)
			Expect(secondErr).To(HaveOccurred())
			Expect(apierrors.IsConflict(secondErr)).To(BeTrue())
			Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring("Warning ApplyConflict")))
			Consistently(fakeRecorder.Events).ShouldNot(Receive())
		})

		It("preserves API-allocated Service fields while updating an operator-owned port", func() {
			service := validService("manage-service-allocated")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			request := reconcile.Request{NamespacedName: clientKey(service)}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			before := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), before)).To(Succeed())
			clusterIP := before.Spec.ClusterIP
			clusterIPs := append([]string(nil), before.Spec.ClusterIPs...)
			ipFamilies := append([]corev1.IPFamily(nil), before.Spec.IPFamilies...)
			ipFamilyPolicy := before.Spec.IPFamilyPolicy

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			storedParent.Spec.Service.Ports[0].Port = 8080
			Expect(k8sClient.Update(ctx, storedParent)).To(Succeed())
			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			after := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), after)).To(Succeed())
			Expect(after.Spec.Ports[0].Port).To(Equal(int32(8080)))
			Expect(after.Spec.ClusterIP).To(Equal(clusterIP))
			Expect(after.Spec.ClusterIPs).To(Equal(clusterIPs))
			Expect(after.Spec.IPFamilies).To(Equal(ipFamilies))
			Expect(after.Spec.IPFamilyPolicy).To(Equal(ipFamilyPolicy))
		})

		It("deletes an owned Service when service management is disabled", func() {
			service := validService("manage-delete-owned-service")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			request := reconcile.Request{NamespacedName: clientKey(service)}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			storedParent.Spec.Service.Enabled = false
			Expect(k8sClient.Update(ctx, storedParent)).To(Succeed())
			_, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			serviceResource := &corev1.Service{}
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), serviceResource))).To(BeTrue())
		})

		It("does not delete an unowned Service when service management is disabled", func() {
			service := validService("manage-preserve-unowned-service")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			service.Spec.Service.Enabled = false

			unownedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: service.Namespace},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": "external"},
					Ports:    []corev1.ServicePort{{Name: "http", Port: 9000}},
				},
			}
			Expect(k8sClient.Create(ctx, unownedService)).To(Succeed())
			serviceRV := unownedService.ResourceVersion
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			preserved := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), preserved)).To(Succeed())
			Expect(preserved.ResourceVersion).To(Equal(serviceRV))
			Expect(preserved.OwnerReferences).To(BeEmpty())

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			ready := findMetav1Condition(storedParent.Status.Conditions, status.ConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Reason).To(Equal(status.ReasonOwnershipConflict))
		})

		It("does not prune an owned Service when the primary Deployment has an ownership conflict", func() {
			service := validService("manage-block-prune")
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			service.Spec.Service.Enabled = false
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			unownedDeployment, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, unownedDeployment)).To(Succeed())

			ownedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:            service.Name,
					Namespace:       service.Namespace,
					OwnerReferences: []metav1.OwnerReference{DesiredOwnerReference(service)},
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": service.Name},
					Ports:    []corev1.ServicePort{{Name: "http", Port: 8888}},
				},
			}
			Expect(k8sClient.Create(ctx, ownedService)).To(Succeed())
			serviceRV := ownedService.ResourceVersion

			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			preservedService := &corev1.Service{}
			Expect(k8sClient.Get(ctx, clientKey(service), preservedService)).To(Succeed())
			Expect(preservedService.ResourceVersion).To(Equal(serviceRV))

			storedParent := &platformv1alpha1.CoffeeShopService{}
			Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
			ready := findMetav1Condition(storedParent.Status.Conditions, status.ConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Reason).To(Equal(status.ReasonOwnershipConflict))
		})
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

func findMetav1Condition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func managedFieldManagers(object metav1.Object) []string {
	managers := make([]string, 0, len(object.GetManagedFields()))
	for _, entry := range object.GetManagedFields() {
		managers = append(managers, entry.Manager)
	}
	return managers
}

// applyExternalDeploymentFields uses SSA with a distinct field manager to model
// a mutating webhook, platform tool, or human-owned field set. ForceOwnership is
// intentionally test-fixture-only: steady-state operator code never forces.
func applyExternalDeploymentFields(
	service *platformv1alpha1.CoffeeShopService,
	fieldManager string,
	fields map[string]any,
) {
	object := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      service.Name,
			"namespace": service.Namespace,
		},
	}
	for key, value := range fields {
		if key == "metadata" {
			metadata := object["metadata"].(map[string]any)
			maps.Copy(metadata, value.(map[string]any))
			continue
		}
		object[key] = value
	}

	configuration := &unstructured.Unstructured{Object: object}
	Expect(k8sClient.Apply(
		ctx,
		client.ApplyConfigurationFromUnstructured(configuration),
		client.FieldOwner(fieldManager),
		client.ForceOwnership,
	)).To(Succeed())
}

func readServiceYAML(relativePath string) *platformv1alpha1.CoffeeShopService {
	path := filepath.Join("..", "..", relativePath)
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	service := &platformv1alpha1.CoffeeShopService{}
	Expect(yaml.Unmarshal(data, service)).To(Succeed())
	return service
}
