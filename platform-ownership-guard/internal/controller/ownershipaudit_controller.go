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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
)

// OwnershipAuditReconciler is intentionally read-only in Slice 6.4.1.
// Slice 6.4.3 adds a narrow OwnershipAudit status writer; inventory and
// detectors never receive a full client.Client.
type OwnershipAuditReconciler struct {
	Reader client.Reader
	Scheme *runtime.Scheme
}

// The manager may observe OwnershipAudit resources, but only the status
// subresource is writable when the status pipeline is introduced.
// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits,verbs=get;list;watch
// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits/status,verbs=get;update;patch

// Reconcile proves the level-based read skeleton without claiming inventory
// or detector behavior that belongs to later slices.
func (r *OwnershipAuditReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	audit := &guardplatformv1alpha1.OwnershipAudit{}
	if err := r.Reader.Get(ctx, req.NamespacedName, audit); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.FromContext(ctx).V(1).Info(
		"Observed OwnershipAudit",
		"name", audit.Name,
		"namespace", audit.Namespace,
		"generation", audit.Generation,
	)

	return ctrl.Result{}, nil
}

// SetupWithManager registers only the guard-owned API watch in Slice 6.4.1.
func (r *OwnershipAuditReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guardplatformv1alpha1.OwnershipAudit{}).
		Named("ownershipaudit").
		Complete(r)
}
