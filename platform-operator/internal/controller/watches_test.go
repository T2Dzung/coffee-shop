package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/status"
)

var _ = Describe("Event-driven watches (Slice 6.2.4)", Ordered, func() {
	var (
		managerContext context.Context
		cancelManager  context.CancelFunc
		managerClient  client.Client
		managerErrors  chan error
	)

	BeforeAll(func() {
		watchManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 clientgoscheme.Scheme,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())

		managerClient = watchManager.GetClient()
		reconciler := &CoffeeShopServiceReconciler{Client: managerClient}
		Expect(reconciler.SetupWithManager(watchManager)).To(Succeed())

		managerContext, cancelManager = context.WithCancel(context.Background())
		managerErrors = make(chan error, 1)
		go func() {
			managerErrors <- watchManager.Start(managerContext)
		}()
		Expect(watchManager.GetCache().WaitForCacheSync(managerContext)).To(BeTrue())
	})

	AfterEach(func() {
		// Remove parents first and wait for the manager cache to observe their
		// deletion. Child delete events can then never recreate resources during
		// test cleanup.
		parents := &platformv1alpha1.CoffeeShopServiceList{}
		Expect(k8sClient.List(ctx, parents)).To(Succeed())
		for i := range parents.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &parents.Items[i]))).To(Succeed())
		}
		Eventually(func() int {
			cachedParents := &platformv1alpha1.CoffeeShopServiceList{}
			if err := managerClient.List(ctx, cachedParents); err != nil {
				return -1
			}
			return len(cachedParents.Items)
		}, 5*time.Second, 50*time.Millisecond).Should(Equal(0))

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

	AfterAll(func() {
		cancelManager()
		Eventually(managerErrors, 5*time.Second).Should(Receive(BeNil()))
	})

	It("recreates a deleted owned Deployment in Manage mode", func() {
		service := validService("watch-manage-delete")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		var oldUID string
		Eventually(func() error {
			deployment := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, clientKey(service), deployment); err != nil {
				return err
			}
			oldUID = string(deployment.UID)
			return nil
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())

		deployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), deployment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, deployment)).To(Succeed())

		Eventually(func() bool {
			recreated := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, clientKey(service), recreated); err != nil {
				return false
			}
			return string(recreated.UID) != oldUID && len(recreated.OwnerReferences) == 1 &&
				recreated.OwnerReferences[0].UID == service.UID
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
	})

	It("recreates a deleted owned Service in Manage mode", func() {
		service := validService("watch-manage-service-delete")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		var oldUID string
		Eventually(func() error {
			kubernetesService := &corev1.Service{}
			if err := k8sClient.Get(ctx, clientKey(service), kubernetesService); err != nil {
				return err
			}
			oldUID = string(kubernetesService.UID)
			return nil
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())

		kubernetesService := &corev1.Service{}
		Expect(k8sClient.Get(ctx, clientKey(service), kubernetesService)).To(Succeed())
		Expect(k8sClient.Delete(ctx, kubernetesService)).To(Succeed())

		Eventually(func() bool {
			recreated := &corev1.Service{}
			if err := k8sClient.Get(ctx, clientKey(service), recreated); err != nil {
				return false
			}
			return string(recreated.UID) != oldUID && len(recreated.OwnerReferences) == 1 &&
				recreated.OwnerReferences[0].UID == service.UID
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
	})

	It("reports a deleted owned Deployment as missing in Observe mode without recreating it", func() {
		service := validService("watch-observe-delete")
		service.Spec.Service.Enabled = false
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		storedParent := &platformv1alpha1.CoffeeShopService{}
		Eventually(func() error {
			return k8sClient.Get(ctx, clientKey(service), storedParent)
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())

		deployment, err := resource.BuildDeployment(storedParent)
		Expect(err).NotTo(HaveOccurred())
		deployment.OwnerReferences = []metav1.OwnerReference{DesiredOwnerReference(storedParent)}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, deployment)).To(Succeed())

		Eventually(func() bool {
			current := &platformv1alpha1.CoffeeShopService{}
			if err := k8sClient.Get(ctx, clientKey(service), current); err != nil {
				return false
			}
			workload := findMetav1Condition(current.Status.Conditions, status.ConditionWorkloadReady)
			return workload != nil && workload.Reason == "WorkloadMissing"
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())

		Consistently(func() bool {
			current := &appsv1.Deployment{}
			return apierrors.IsNotFound(k8sClient.Get(ctx, clientKey(service), current))
		}, 500*time.Millisecond, 50*time.Millisecond).Should(BeTrue())
	})

	It("reconciles a Manage CR when an unowned collision is deleted", func() {
		service := validService("watch-collision-delete")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage

		collision, err := resource.BuildDeployment(service)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Create(ctx, collision)).To(Succeed())
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		Eventually(func() bool {
			current := &platformv1alpha1.CoffeeShopService{}
			if err := k8sClient.Get(ctx, clientKey(service), current); err != nil {
				return false
			}
			ready := findMetav1Condition(current.Status.Conditions, status.ConditionReady)
			return ready != nil && ready.Reason == status.ReasonOwnershipConflict
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, collision)).To(Succeed())

		Eventually(func() bool {
			recreated := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, clientKey(service), recreated); err != nil {
				return false
			}
			return len(recreated.OwnerReferences) == 1 && recreated.OwnerReferences[0].UID == service.UID
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
	})

	It("maps Deployment availability status changes back to parent status", func() {
		service := validService("watch-availability")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		deployment := &appsv1.Deployment{}
		Eventually(func() error {
			return k8sClient.Get(ctx, clientKey(service), deployment)
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		deployment.Status.Replicas = service.Spec.Replicas
		deployment.Status.ReadyReplicas = service.Spec.Replicas
		deployment.Status.AvailableReplicas = service.Spec.Replicas
		Expect(k8sClient.Status().Update(ctx, deployment)).To(Succeed())

		Eventually(func() bool {
			current := &platformv1alpha1.CoffeeShopService{}
			if err := k8sClient.Get(ctx, clientKey(service), current); err != nil {
				return false
			}
			workload := findMetav1Condition(current.Status.Conditions, status.ConditionWorkloadReady)
			return current.Status.ReadyReplicas == service.Spec.Replicas &&
				workload != nil && workload.Status == metav1.ConditionTrue
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
	})

	It("does not reconcile again for its own parent status patch", func() {
		service := validService("watch-status-loop")
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		Eventually(func() bool {
			current := &platformv1alpha1.CoffeeShopService{}
			if err := k8sClient.Get(ctx, clientKey(service), current); err != nil {
				return false
			}
			return current.Status.ObservedGeneration == current.Generation
		}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())

		baseline := controllerReconcileTotal("coffeeshopservice")
		Consistently(
			func() float64 { return controllerReconcileTotal("coffeeshopservice") },
			500*time.Millisecond,
			50*time.Millisecond,
		).Should(Equal(baseline))
	})

	It("filters an irrelevant child annotation update before enqueue", func() {
		service := validService("watch-annotation-filter")
		service.Spec.ManagementPolicy = platformv1alpha1.ManagementPolicyManage
		Expect(k8sClient.Create(ctx, service)).To(Succeed())

		Eventually(func() error {
			return k8sClient.Get(ctx, clientKey(service), &appsv1.Deployment{})
		}, 5*time.Second, 50*time.Millisecond).Should(Succeed())
		waitForReconcileCountToStabilize("coffeeshopservice")
		baseline := controllerReconcileTotal("coffeeshopservice")

		deployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, clientKey(service), deployment)).To(Succeed())
		base := deployment.DeepCopy()
		deployment.Annotations = map[string]string{"example.com/note": "keep"}
		Expect(k8sClient.Patch(ctx, deployment, client.MergeFrom(base))).To(Succeed())

		Consistently(
			func() float64 { return controllerReconcileTotal("coffeeshopservice") },
			500*time.Millisecond,
			50*time.Millisecond,
		).Should(Equal(baseline))
	})
})

func controllerReconcileTotal(controllerName string) float64 {
	families, err := crmetrics.Registry.Gather()
	if err != nil {
		return -1
	}

	var total float64
	for _, family := range families {
		if family.GetName() != "controller_runtime_reconcile_total" {
			continue
		}
		for _, metric := range family.Metric {
			matchesController := false
			for _, label := range metric.Label {
				if label.GetName() == "controller" && label.GetValue() == controllerName {
					matchesController = true
					break
				}
			}
			if matchesController && metric.Counter != nil {
				total += metric.Counter.GetValue()
			}
		}
	}
	return total
}

func waitForReconcileCountToStabilize(controllerName string) {
	previous := -1.0
	stableSamples := 0
	Eventually(func() bool {
		current := controllerReconcileTotal(controllerName)
		if current == previous {
			stableSamples++
		} else {
			previous = current
			stableSamples = 0
		}
		return stableSamples >= 3
	}, 5*time.Second, 50*time.Millisecond).Should(BeTrue())
}
