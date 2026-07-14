package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/status"
)

// OwnershipResult classifies a live child resource from the operator's perspective.
type OwnershipResult = status.OwnershipResult

const (
	// OwnershipAbsent means the child does not exist.
	OwnershipAbsent = status.OwnershipAbsent
	// OwnershipOwned means the child has a controller ownerReference pointing to
	// this CR with the correct UID.
	OwnershipOwned = status.OwnershipOwned
	// OwnershipUnownedCollision means the child exists but has no controller
	// ownerReference.
	OwnershipUnownedCollision = status.OwnershipUnownedCollision
	// OwnershipForeignOwnedCollision means the child has a controller
	// ownerReference from a different controller/owner.
	OwnershipForeignOwnedCollision = status.OwnershipForeignOwnedCollision
	// OwnershipStaleOwnerCollision means the child has a controller ownerReference
	// with the same name but a different (stale) UID.
	OwnershipStaleOwnerCollision = status.OwnershipStaleOwnerCollision
)

// ClassifyOwnership determines the ownership state of a live child resource
// relative to the parent CoffeeShopService.
//
// The classification is based purely on UID checks of the controller
// ownerReference. Label-based "managed-by" checks are NOT a substitute
// for UID verification (plan Section 7.3).
func ClassifyOwnership(parent *platformv1alpha1.CoffeeShopService, childOwnerRefs []metav1.OwnerReference, childExists bool) OwnershipResult {
	if !childExists {
		return OwnershipAbsent
	}

	controllerRef := controllerOwnerRef(childOwnerRefs)
	if controllerRef == nil {
		return OwnershipUnownedCollision
	}

	// Check if the controller owner is from our API group
	if !isOurGroupKind(controllerRef) {
		return OwnershipForeignOwnedCollision
	}

	// Same group/kind — check name and UID
	if controllerRef.Name == parent.Name && controllerRef.UID == parent.UID {
		return OwnershipOwned
	}

	// Same name but different UID — stale owner from a previous CR incarnation
	if controllerRef.Name == parent.Name && controllerRef.UID != parent.UID {
		return OwnershipStaleOwnerCollision
	}

	// Different name entirely — foreign owner within same group
	return OwnershipForeignOwnedCollision
}

// ClassifyDeploymentOwnership is a convenience wrapper for Deployment children.
func ClassifyDeploymentOwnership(parent *platformv1alpha1.CoffeeShopService, deploy *appsv1.Deployment, exists bool) OwnershipResult {
	if !exists || deploy == nil {
		return ClassifyOwnership(parent, nil, false)
	}
	return ClassifyOwnership(parent, deploy.OwnerReferences, true)
}

// ClassifyServiceOwnership is a convenience wrapper for Service children.
func ClassifyServiceOwnership(parent *platformv1alpha1.CoffeeShopService, svc *corev1.Service, exists bool) OwnershipResult {
	if !exists || svc == nil {
		return ClassifyOwnership(parent, nil, false)
	}
	return ClassifyOwnership(parent, svc.OwnerReferences, true)
}

// controllerOwnerRef finds the first ownerReference with Controller=true.
func controllerOwnerRef(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

// isOurGroupKind checks if the ownerReference belongs to our CRD group/kind.
func isOurGroupKind(ref *metav1.OwnerReference) bool {
	return ref.APIVersion == platformv1alpha1.GroupVersion.String() &&
		ref.Kind == "CoffeeShopService"
}

// DesiredOwnerReference builds the controller ownerReference that the operator
// sets on children it creates via SSA.
func DesiredOwnerReference(parent *platformv1alpha1.CoffeeShopService) metav1.OwnerReference {
	isController := true
	return metav1.OwnerReference{
		APIVersion: platformv1alpha1.GroupVersion.String(),
		Kind:       "CoffeeShopService",
		Name:       parent.Name,
		UID:        parent.UID,
		Controller: &isController,
	}
}

// OwnerUID returns the parent's UID for quick comparison.
func OwnerUID(parent *platformv1alpha1.CoffeeShopService) types.UID {
	return parent.UID
}
