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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

// OwnershipAuditReconciler owns only OwnershipAudit status writes.
type OwnershipAuditReconciler struct {
	Reader        client.Reader
	StatusWriter  client.SubResourceWriter
	Collector     inventory.InventoryCollector
	Evaluator     FoundationEvaluator
	Scheme        *runtime.Scheme
	StatusBuilder *StatusBuilder
	Now           func() time.Time
}

// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits,verbs=get;list;watch
// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits/status,verbs=get;update;patch

func (r *OwnershipAuditReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Reader == nil || r.Collector == nil || r.StatusWriter == nil {
		return ctrl.Result{}, fmt.Errorf("reconciler dependencies Reader, Collector, and StatusWriter are required")
	}

	audit := &guardplatformv1alpha1.OwnershipAudit{}
	if err := r.Reader.Get(ctx, req.NamespacedName, audit); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	now := r.Now
	if now == nil {
		now = time.Now
	}
	builder := r.StatusBuilder
	if builder == nil {
		builder = NewStatusBuilder(now)
	}
	evaluator := r.Evaluator
	if evaluator == nil {
		evaluator = NoopFoundationEvaluator{}
	}

	snapshot, collectErr := r.Collector.Collect(ctx, audit.Namespace, &audit.Spec)
	var overrideReason, overrideMessage string
	var transientErr error
	if collectErr != nil {
		if typed, ok := collectErr.(*inventory.InventoryError); ok {
			overrideReason = string(typed.DTO.Class)
			overrideMessage = typed.DTO.Message
			if typed.DTO.Class == inventory.ErrTransientReadFailure {
				transientErr = collectErr
			}
		} else {
			overrideReason = string(inventory.ErrTransientReadFailure)
			overrideMessage = collectErr.Error()
			transientErr = collectErr
		}
	}

	findings := evaluator.Evaluate(snapshot)
	desired := builder.BuildStatus(
		&audit.Status,
		audit.Generation,
		snapshot,
		findings,
		audit.Spec.ResyncInterval.Duration,
		overrideReason,
		overrideMessage,
	)

	if !SemanticEqualStatus(&audit.Status, desired) || heartbeatChanged(audit.Status.LastCompletedScanTime, desired.LastCompletedScanTime) {
		if err := r.patchStatus(ctx, audit, desired); err != nil {
			log.FromContext(ctx).Error(err, "Failed to patch OwnershipAudit status")
			return ctrl.Result{}, err
		}
	}

	if transientErr != nil {
		return ctrl.Result{}, transientErr
	}

	resync := audit.Spec.ResyncInterval.Duration
	if resync <= 0 {
		resync = 10 * time.Minute
	}
	return ctrl.Result{RequeueAfter: resync}, nil
}

// patchStatus uses the object read for evaluation as the merge base.
// A conflict is returned so the next workqueue attempt re-reads and re-evaluates.
func (r *OwnershipAuditReconciler) patchStatus(
	ctx context.Context,
	audit *guardplatformv1alpha1.OwnershipAudit,
	desired *guardplatformv1alpha1.OwnershipAuditStatus,
) error {
	base := audit.DeepCopy()
	audit.Status = *desired.DeepCopy()
	return r.StatusWriter.Patch(ctx, audit, client.MergeFrom(base))
}

func heartbeatChanged(oldTime, newTime *metav1.Time) bool {
	if oldTime == nil || newTime == nil {
		return oldTime != nil || newTime != nil
	}
	return !oldTime.Equal(newTime)
}

func (r *OwnershipAuditReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guardplatformv1alpha1.OwnershipAudit{}).
		Named("ownershipaudit").
		Complete(r)
}
