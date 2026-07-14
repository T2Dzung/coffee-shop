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
	"k8s.io/client-go/tools/record"
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
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=platform.t2dzung.github.io,resources=coffeeshopservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=platform.t2dzung.github.io,resources=coffeeshopservices/status,verbs=get;patch
// Delete is required by OwnerReferencesPermissionEnforcement when the manager
// sets metadata.ownerReferences; the reconciler does not directly delete Deployments.
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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

	log.V(1).Info("Reconciling in Manage mode")
	return r.reconcileManage(ctx, service)
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

// reconcileManage implements the Manage (reconciliation) pipeline:
// 1. Defensive validation
// 2. Get live children
// 3. Perform ownership checks and mutation (create/reconcile child or delete service)
// 4. Map outcomes to status and patch parent CR status
func (r *CoffeeShopServiceReconciler) reconcileManage(ctx context.Context, service *platformv1alpha1.CoffeeShopService) (ctrl.Result, error) {
	// 1. Defensive validation
	if err := resource.Validate(service); err != nil {
		delta := status.CalculateInvalidSpecStatus(service, err.Error())
		return r.applyStatusDelta(ctx, service, delta)
	}

	// 2. Observe live state
	obs, err := ObserveLiveState(ctx, r, service)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Reconcile child mutations with safety checks
	var applyResult *ApplyResult
	var pruneConflict status.OwnershipResult
	var serviceDeleted bool
	var applyErr error

	serviceDisabled := service.Spec.Service == nil || !service.Spec.Service.Enabled

	// Apply the desired set before pruning. A primary Deployment conflict or
	// apply failure blocks Service deletion, preventing a partial mutation that
	// could remove networking while the workload identity is unresolved.
	applyResult, applyErr = ApplyDesiredChildren(ctx, r.Client, r.Scheme(), service, obs)
	r.recordApplyOwnershipConflicts(service, applyResult)

	// Cleanup a disabled Service only after the desired Deployment converged
	// without an ownership conflict.
	if applyErr == nil && applyResult != nil && !applyResult.HasConflict() && serviceDisabled && obs.ServiceExists {
		deleted, conflict, err := DeleteOwnedService(ctx, r.Client, service, obs)
		if err != nil {
			applyErr = err
		} else {
			serviceDeleted = deleted
			pruneConflict = conflict
			if conflict != "" && r.Recorder != nil {
				r.Recorder.Eventf(service, "Warning", "OwnershipConflict", "Service deletion blocked by collision: %s", conflict)
			}
		}
	}

	// 4. Update parent Status to reflect the outcomes
	obsInput := &status.ObservationInput{
		DeploymentExists:  obs.DeploymentExists,
		DeploymentDrifted: obs.DeploymentDrifted,
		LiveDeployment:    obs.Deployment,
		ServiceExists:     obs.ServiceExists,
		ServiceDrifted:    obs.ServiceDrifted,
		LiveService:       obs.Service,
		ServiceEnabled:    !serviceDisabled,
	}

	// If child mutations were applied successfully, we optimistically reflect the new existence in status calculation
	// to avoid status lag, but we still rely on the actual live status for readiness fields.
	if applyResult != nil {
		if applyResult.DeploymentApplied {
			obsInput.DeploymentExists = true
		}
		if applyResult.ServiceApplied {
			obsInput.ServiceExists = true
		}
	}
	if serviceDeleted {
		obsInput.ServiceExists = false
		obsInput.LiveService = nil
	}

	manageInput := &status.ManageInput{
		Obs: obsInput,
	}
	if applyResult != nil {
		manageInput.DeploymentConflict = applyResult.DeploymentConflict
		manageInput.ServiceConflict = applyResult.ServiceConflict
	}
	manageInput.PruneConflict = pruneConflict
	if applyErr != nil {
		manageInput.ApplyError = applyErr.Error()
		manageInput.ApplyErrorReason = status.ReasonApplyFailed
		if apierrors.IsConflict(applyErr) {
			manageInput.ApplyErrorReason = status.ReasonApplyConflict
		}
	}

	delta := status.CalculateManageStatus(service, manageInput)

	recordApplyFailure := applyErr != nil && r.Recorder != nil && readyReasonChanged(service, manageInput.ApplyErrorReason)

	statusResult, statusErr := r.applyStatusDelta(ctx, service, delta)
	if statusErr != nil {
		return ctrl.Result{}, statusErr
	}

	if applyErr != nil {
		// Emit only after the failure condition is persisted. Returning applyErr
		// delegates retry timing to the workqueue rate limiter; the unchanged
		// condition suppresses duplicate Events on persistent retries.
		if recordApplyFailure {
			r.Recorder.Eventf(service, "Warning", manageInput.ApplyErrorReason, "Child reconciliation failed: %v", applyErr)
		}
		return ctrl.Result{}, applyErr
	}

	// If there's an ownership conflict, return success (no requeue) and wait for user correction
	if manageInput.DeploymentConflict != "" || manageInput.ServiceConflict != "" || manageInput.PruneConflict != "" {
		return ctrl.Result{}, nil
	}

	return statusResult, nil
}

func (r *CoffeeShopServiceReconciler) recordApplyOwnershipConflicts(
	service *platformv1alpha1.CoffeeShopService,
	result *ApplyResult,
) {
	if result == nil || r.Recorder == nil {
		return
	}
	if result.DeploymentConflict != "" {
		r.Recorder.Eventf(service, "Warning", "OwnershipConflict", "Deployment apply blocked by collision: %s", result.DeploymentConflict)
	}
	if result.ServiceConflict != "" {
		r.Recorder.Eventf(service, "Warning", "OwnershipConflict", "Service apply blocked by collision: %s", result.ServiceConflict)
	}
}

func readyReasonChanged(service *platformv1alpha1.CoffeeShopService, reason string) bool {
	for i := range service.Status.Conditions {
		condition := service.Status.Conditions[i]
		if condition.Type == status.ConditionReady {
			return condition.Status != metav1.ConditionFalse || condition.Reason != reason
		}
	}
	return true
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
	r.Recorder = mgr.GetEventRecorderFor("coffeeshopservice")
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.CoffeeShopService{}).
		Named("coffeeshopservice").
		Complete(r)
}
