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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// CoffeeShopServiceReconciler is intentionally read-only through Phase 6.1.
// Child reconciliation, status writes, SSA and adoption start at the Phase 6.2 gate.
type CoffeeShopServiceReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=platform.t2dzung.github.io,resources=coffeeshopservices,verbs=get;list;watch

// Reconcile verifies that the CR can be read but performs no child or status writes.
func (r *CoffeeShopServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	service := &platformv1alpha1.CoffeeShopService{}
	if err := r.Get(ctx, req.NamespacedName, service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logf.FromContext(ctx).V(1).Info(
		"Validated CoffeeShopService API object; reconciliation is deferred to Phase 6.2",
		"managementPolicy", service.Spec.ManagementPolicy,
	)
	return ctrl.Result{}, nil
}

// SetupWithManager registers only the primary resource watch for Phase 6.1.
func (r *CoffeeShopServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.CoffeeShopService{}).
		Named("coffeeshopservice").
		Complete(r)
}
