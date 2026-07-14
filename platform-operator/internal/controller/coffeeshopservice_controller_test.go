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

func readServiceYAML(relativePath string) *platformv1alpha1.CoffeeShopService {
	path := filepath.Join("..", "..", relativePath)
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	service := &platformv1alpha1.CoffeeShopService{}
	Expect(yaml.Unmarshal(data, service)).To(Succeed())
	return service
}
