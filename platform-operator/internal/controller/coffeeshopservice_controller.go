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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/status"
)

// CoffeeShopServiceReconciler reconciles a CoffeeShopService object.
type CoffeeShopServiceReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=platform.t2dzung.github.io,resources=coffeeshopservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=platform.t2dzung.github.io,resources=coffeeshopservices/status,verbs=get;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// Reconcile is the main execution loop of the controller.
func (r *CoffeeShopServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	service := &platformv1alpha1.CoffeeShopService{}
	if err := r.Get(ctx, req.NamespacedName, service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Route reconciliation based on ManagementPolicy
	if service.Spec.ManagementPolicy == platformv1alpha1.ManagementPolicyObserve {
		log.V(1).Info("Reconciling in Observe mode")
		return r.reconcileObserve(ctx, service)
	}

	// Manage mode deferred for Slice 6.2.2+
	log.V(1).Info("Reconciliation in Manage mode is deferred to Slice 6.2.2")
	return ctrl.Result{}, nil
}

// reconcileObserve implements the Observe-only pipeline:
// 1. Defensive validation
// 2. Get live children (zero mutation)
// 3. Calculate semantic status via status.CalculateObserveStatus
// 4. Patch status only when changed
func (r *CoffeeShopServiceReconciler) reconcileObserve(ctx context.Context, service *platformv1alpha1.CoffeeShopService) (ctrl.Result, error) {
	// 1. Defensive validation
	if err := resource.Validate(service); err != nil {
		delta := status.CalculateInvalidSpecStatus(service, err.Error())
		return r.applyStatusDelta(ctx, service, delta)
	}

	// 2. Observe live state (zero child writes)
	obs, err := ObserveLiveState(ctx, r, service)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Map observation to status calculation input
	input := &status.ObservationInput{
		DeploymentExists:  obs.DeploymentExists,
		DeploymentDrifted: obs.DeploymentDrifted,
		LiveDeployment:    obs.Deployment,
		ServiceExists:     obs.ServiceExists,
		ServiceDrifted:    obs.ServiceDrifted,
		LiveService:       obs.Service,
		ServiceEnabled:    service.Spec.Service != nil && service.Spec.Service.Enabled,
	}

	delta := status.CalculateObserveStatus(service, input)

	// 4. Patch status only when semantically changed
	return r.applyStatusDelta(ctx, service, delta)
}

// applyStatusDelta applies a StatusDelta to the CoffeeShopService and patches
// the status subresource only if there is a semantic change.
func (r *CoffeeShopServiceReconciler) applyStatusDelta(ctx context.Context, service *platformv1alpha1.CoffeeShopService, delta *status.StatusDelta) (ctrl.Result, error) {
	now := metav1.Now()
	oldStatus := service.Status.DeepCopy()

	service.Status.ObservedGeneration = delta.ObservedGeneration
	service.Status.DesiredReplicas = delta.DesiredReplicas
	service.Status.ReadyReplicas = delta.ReadyReplicas

	statusChanged := false
	for _, cond := range delta.Conditions {
		changed := status.SetCondition(service, cond.Type, cond.Status, cond.Reason, cond.Message, now)
		statusChanged = statusChanged || changed
	}

	// Check scalar fields change
	if oldStatus.ObservedGeneration != service.Status.ObservedGeneration ||
		oldStatus.DesiredReplicas != service.Status.DesiredReplicas ||
		oldStatus.ReadyReplicas != service.Status.ReadyReplicas {
		statusChanged = true
	}

	if !statusChanged {
		return ctrl.Result{}, nil
	}

	// Use MergeFrom with a copy that carries the old status for correct diff
	base := service.DeepCopy()
	base.Status = *oldStatus
	patch := client.MergeFrom(base)
	if err := r.Status().Patch(ctx, service, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the primary resource watch.
func (r *CoffeeShopServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.CoffeeShopService{}).
		Named("coffeeshopservice").
		Complete(r)
}
