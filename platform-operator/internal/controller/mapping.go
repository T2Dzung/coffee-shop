package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// mapDeletedCollisionToParent wakes a same-name Manage CR when an unowned,
// foreign-owned, or stale-owned collision disappears. Owned deletes are already
// handled by .Owns() and therefore return no request here.
func (r *CoffeeShopServiceReconciler) mapDeletedCollisionToParent(
	ctx context.Context,
	object client.Object,
) []reconcile.Request {
	parent := &platformv1alpha1.CoffeeShopService{}
	key := client.ObjectKeyFromObject(object)
	if err := r.Get(ctx, key, parent); err != nil {
		if !apierrors.IsNotFound(err) {
			// A watch mapper cannot return an error. Enqueue the deterministic
			// same-name key so Reconcile can surface/retry a transient read error
			// instead of losing the only collision-delete event.
			logf.FromContext(ctx).Error(err, "Could not map deleted collision to CoffeeShopService", "object", key)
			return []reconcile.Request{{NamespacedName: key}}
		}
		return nil
	}
	if parent.Spec.ManagementPolicy != platformv1alpha1.ManagementPolicyManage {
		return nil
	}

	var ownership OwnershipResult
	switch child := object.(type) {
	case *appsv1.Deployment:
		ownership = ClassifyDeploymentOwnership(parent, child, true)
	case *corev1.Service:
		ownership = ClassifyServiceOwnership(parent, child, true)
	default:
		return nil
	}
	if ownership == OwnershipOwned {
		return nil
	}

	return []reconcile.Request{{NamespacedName: key}}
}
