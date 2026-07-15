package controller

import (
	"context"
	"errors"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/status"
)

const legacyImage = "registry.example/web:legacy"

var _ = Describe("Explicit adoption (Slice 6.2.5)", func() {
	AfterEach(func() {
		parents := &platformv1alpha1.CoffeeShopServiceList{}
		Expect(k8sClient.List(ctx, parents)).To(Succeed())
		for i := range parents.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &parents.Items[i]))).To(Succeed())
		}

		deployments := &appsv1.DeploymentList{}
		Expect(k8sClient.List(ctx, deployments)).To(Succeed())
		for i := range deployments.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &deployments.Items[i]))).To(Succeed())
		}
		services := &corev1.ServiceList{}
		Expect(k8sClient.List(ctx, services)).To(Succeed())
		for i := range services.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &services.Items[i]))).To(Succeed())
		}
	})

	DescribeTable("requires both explicit policy and the exact child annotation",
		func(
			name string,
			policy platformv1alpha1.AdoptionPolicy,
			annotation *string,
			expectAdopted bool,
			expectAdoptionEvent string,
		) {
			service := validService(name)
			service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
			service.Spec.AdoptionPolicy = policy
			service.Spec.Service.Enabled = false

			deployment, err := resource.BuildDeployment(service)
			Expect(err).NotTo(HaveOccurred())
			if annotation != nil {
				deployment.Annotations = map[string]string{AdoptionAnnotationKey: *annotation}
			}
			Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
			originalRV := deployment.ResourceVersion
			Expect(k8sClient.Create(ctx, service)).To(Succeed())

			fakeRecorder := record.NewFakeRecorder(4)
			reconciler := &CoffeeShopServiceReconciler{Client: k8sClient, Recorder: fakeRecorder}
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
			Expect(err).NotTo(HaveOccurred())

			stored := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
			if expectAdopted {
				Expect(stored.OwnerReferences).To(HaveLen(1))
				Expect(stored.OwnerReferences[0].UID).To(Equal(service.UID))
			} else {
				Expect(stored.OwnerReferences).To(BeEmpty())
				Expect(stored.ResourceVersion).To(Equal(originalRV))

				parent := &platformv1alpha1.CoffeeShopService{}
				Expect(k8sClient.Get(ctx, clientKey(service), parent)).To(Succeed())
				ready := findMetav1Condition(parent.Status.Conditions, status.ConditionReady)
				Expect(ready).NotTo(BeNil())
				Expect(ready.Reason).To(Equal(status.ReasonOwnershipConflict))
			}
			if expectAdoptionEvent != "" {
				Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring(expectAdoptionEvent)))
			}
		},
		Entry(
			"Never with correct annotation",
			"adopt-never",
			platformv1alpha1.AdoptionPolicyNever,
			new("adopt-never"),
			false,
			"",
		),
		Entry(
			"Explicit without annotation",
			"adopt-missing",
			platformv1alpha1.AdoptionPolicyExplicit,
			nil,
			false,
			"Warning AdoptionRejected",
		),
		Entry(
			"Explicit with wrong annotation",
			"adopt-wrong",
			platformv1alpha1.AdoptionPolicyExplicit,
			new("another-parent"),
			false,
			"Warning AdoptionRejected",
		),
		Entry(
			"Explicit with correct annotation",
			"adopt-correct",
			platformv1alpha1.AdoptionPolicyExplicit,
			new("adopt-correct"),
			true,
			"Normal AdoptionSucceeded",
		),
	)

	It("rejects a child controlled by another owner even with both opt-ins", func() {
		service := validService("adopt-foreign-owner")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit
		service.Spec.Service.Enabled = false

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		controller := true
		deployment.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
			Name:       "foreign",
			UID:        "foreign-uid",
			Controller: &controller,
		}}
		deployment.Annotations = map[string]string{AdoptionAnnotationKey: service.Name}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		originalRV := deployment.ResourceVersion
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
		Expect(err).NotTo(HaveOccurred())

		stored := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.ResourceVersion).To(Equal(originalRV))
		Expect(stored.OwnerReferences[0].UID).To(Equal(deployment.OwnerReferences[0].UID))
	})

	It("rejects an incompatible immutable Deployment selector without mutation", func() {
		service := validService("adopt-selector-mismatch")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit
		service.Spec.Service.Enabled = false

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		deployment.Spec.Selector.MatchLabels = map[string]string{"app": "external"}
		deployment.Spec.Template.Labels["app"] = "external"
		deployment.Annotations = map[string]string{AdoptionAnnotationKey: service.Name}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		originalRV := deployment.ResourceVersion
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
		Expect(err).NotTo(HaveOccurred())

		stored := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.ResourceVersion).To(Equal(originalRV))
		Expect(stored.OwnerReferences).To(BeEmpty())
		Expect(stored.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app", "external"))
	})

	It("rejects incompatible Service ports during in-memory preflight", func() {
		service := validService("adopt-service-port-mismatch")
		desired, err := resource.BuildService(service)
		Expect(err).NotTo(HaveOccurred())
		live := desired.DeepCopy()
		live.Spec.Ports[0].Port = 9999

		Expect(preflightServiceAdoption(service, live, desired)).To(
			MatchError("service ports differ from desired"),
		)
	})

	It("leaves the live child untouched when the adoption dry-run conflicts", func() {
		service := validService("adopt-dry-run-conflict")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit
		service.Spec.Service.Enabled = false

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		deployment.Annotations = map[string]string{AdoptionAnnotationKey: service.Name}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		originalRV := deployment.ResourceVersion
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{
			Client: &dryRunConflictClient{Client: k8sClient},
		}
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsConflict(err)).To(BeTrue())

		stored := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.ResourceVersion).To(Equal(originalRV))
		Expect(stored.OwnerReferences).To(BeEmpty())

		parent := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), parent)).To(Succeed())
		ready := findMetav1Condition(parent.Status.Conditions, status.ConditionReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal(status.ReasonApplyConflict))
	})

	It("leaves the live child untouched when the adoption commit fails", func() {
		service := validService("adopt-commit-failure")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit
		service.Spec.Service.Enabled = false

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		deployment.Spec.Template.Spec.Containers[0].Image = legacyImage
		deployment.Annotations = map[string]string{AdoptionAnnotationKey: service.Name}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		originalRV := deployment.ResourceVersion
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{
			Client: &commitFailureClient{Client: k8sClient},
		}
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: clientKey(service)})
		Expect(err).To(MatchError(ContainSubstring("adoption Commit failed")))

		stored := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), stored)).To(Succeed())
		Expect(stored.ResourceVersion).To(Equal(originalRV))
		Expect(stored.OwnerReferences).To(BeEmpty())
		Expect(stored.Spec.Template.Spec.Containers[0].Image).To(Equal(legacyImage))

		parent := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), parent)).To(Succeed())
		ready := findMetav1Condition(parent.Status.Conditions, status.ConditionReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Reason).To(Equal(status.ReasonAdoptionFailed))
	})

	It("adopts Deployment then Service in separate reconciliations and preserves user metadata", func() {
		service := validService("adopt-staged")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		deployment.Spec.Template.Spec.Containers[0].Image = legacyImage
		deployment.Annotations = map[string]string{
			AdoptionAnnotationKey: service.Name,
			"example.com/owner":   "platform-team",
		}
		deployment.Labels["example.com/tier"] = "edge"
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())

		kubernetesService, err := resource.BuildService(service)
		Expect(err).NotTo(HaveOccurred())
		kubernetesService.Annotations = map[string]string{
			AdoptionAnnotationKey: service.Name,
			"example.com/owner":   "platform-team",
		}
		Expect(k8sClient.Create(ctx, kubernetesService)).To(Succeed())
		originalServiceRV := kubernetesService.ResourceVersion
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		fakeRecorder := record.NewFakeRecorder(8)
		reconciler := &CoffeeShopServiceReconciler{Client: k8sClient, Recorder: fakeRecorder}
		request := reconcile.Request{NamespacedName: clientKey(service)}

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		adoptedDeployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), adoptedDeployment)).To(Succeed())
		Expect(adoptedDeployment.OwnerReferences[0].UID).To(Equal(service.UID))
		Expect(adoptedDeployment.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/web:v1.0.0"))
		Expect(adoptedDeployment.Annotations).To(HaveKeyWithValue("example.com/owner", "platform-team"))
		Expect(adoptedDeployment.Labels).To(HaveKeyWithValue("example.com/tier", "edge"))

		stillUnownedService := &corev1.Service{}
		Expect(k8sClient.Get(ctx, clientKey(service), stillUnownedService)).To(Succeed())
		Expect(stillUnownedService.ResourceVersion).To(Equal(originalServiceRV))
		Expect(stillUnownedService.OwnerReferences).To(BeEmpty())

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		adoptedService := &corev1.Service{}
		Expect(k8sClient.Get(ctx, clientKey(service), adoptedService)).To(Succeed())
		Expect(adoptedService.OwnerReferences[0].UID).To(Equal(service.UID))
		Expect(adoptedService.Annotations).To(HaveKeyWithValue("example.com/owner", "platform-team"))
		Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring("Normal AdoptionSucceeded")))
		Eventually(fakeRecorder.Events).Should(Receive(ContainSubstring("Normal AdoptionSucceeded")))
	})

	It("continues steady-state no-force reconciliation after the user resets policy to Never", func() {
		service := validService("adopt-reset-never")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		service.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyExplicit
		service.Spec.Service.Enabled = false

		deployment, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		// Make an operator-owned field differ so the one-time forced adoption
		// actually transfers SSA ownership from controller.test. Applying the
		// same value would leave the field co-owned and would not prove transfer.
		deployment.Spec.Template.Spec.Containers[0].Image = legacyImage
		deployment.Annotations = map[string]string{
			AdoptionAnnotationKey: service.Name,
			"example.com/owner":   "platform-team",
		}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		reconciler := &CoffeeShopServiceReconciler{Client: k8sClient}
		request := reconcile.Request{NamespacedName: clientKey(service)}
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		storedParent := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), storedParent)).To(Succeed())
		storedParent.Spec.AdoptionPolicy = platformv1alpha1.AdoptionPolicyNever
		storedParent.Spec.Image.Tag = "v2.0.0"
		Expect(k8sClient.Update(ctx, storedParent)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		storedDeployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), storedDeployment)).To(Succeed())
		Expect(storedDeployment.OwnerReferences[0].UID).To(Equal(service.UID))
		Expect(storedDeployment.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example/web:v2.0.0"))
		Expect(storedDeployment.Annotations).To(HaveKeyWithValue("example.com/owner", "platform-team"))

		refreshedParent := &platformv1alpha1.CoffeeShopService{}
		Expect(k8sClient.Get(ctx, clientKey(service), refreshedParent)).To(Succeed())
		Expect(refreshedParent.Spec.AdoptionPolicy).To(Equal(platformv1alpha1.AdoptionPolicyNever))
	})
})

type dryRunConflictClient struct {
	client.Client
}

func (c *dryRunConflictClient) Apply(
	ctx context.Context,
	object runtime.ApplyConfiguration,
	options ...client.ApplyOption,
) error {
	applyOptions := (&client.ApplyOptions{}).ApplyOptions(options)
	if slices.Contains(applyOptions.DryRun, metav1.DryRunAll) {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "apps", Resource: "deployments"},
			"adoption-fixture",
			errors.New("injected dry-run conflict"),
		)
	}
	return c.Client.Apply(ctx, object, options...)
}

type commitFailureClient struct {
	client.Client
}

func (c *commitFailureClient) Apply(
	ctx context.Context,
	object runtime.ApplyConfiguration,
	options ...client.ApplyOption,
) error {
	applyOptions := (&client.ApplyOptions{}).ApplyOptions(options)
	if !slices.Contains(applyOptions.DryRun, metav1.DryRunAll) {
		return errors.New("injected commit failure")
	}
	return c.Client.Apply(ctx, object, options...)
}
