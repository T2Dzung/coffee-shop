package controller

import (
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ParentGenerationPredicate ignores status-only parent updates. The status
// subresource does not increment metadata.generation, so a status patch cannot
// enqueue the same parent indefinitely.
func ParentGenerationPredicate() predicate.Predicate {
	return predicate.GenerationChangedPredicate{}
}

// RelevantChildChangePredicate accepts events that can change reconciliation
// behavior: lifecycle, spec, status, labels, or ownerReferences. Annotations are
// deliberately excluded because they are outside the Phase 6.2 steady-state
// intent; adoption annotations are introduced with an explicit predicate in
// Slice 6.2.5.
func RelevantChildChangePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			switch oldObject := e.ObjectOld.(type) {
			case *appsv1.Deployment:
				newObject, ok := e.ObjectNew.(*appsv1.Deployment)
				return ok && deploymentChangeRelevant(oldObject, newObject)
			case *corev1.Service:
				newObject, ok := e.ObjectNew.(*corev1.Service)
				return ok && serviceChangeRelevant(oldObject, newObject)
			default:
				return false
			}
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// CollisionDeletePredicate feeds only delete tombstones to the explicit
// collision mapper. Owned lifecycle/spec/status events use .Owns().
func CollisionDeletePredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func deploymentChangeRelevant(oldObject, newObject *appsv1.Deployment) bool {
	return !reflect.DeepEqual(oldObject.Spec, newObject.Spec) ||
		!reflect.DeepEqual(oldObject.Status, newObject.Status) ||
		!reflect.DeepEqual(oldObject.Labels, newObject.Labels) ||
		!reflect.DeepEqual(oldObject.OwnerReferences, newObject.OwnerReferences)
}

func serviceChangeRelevant(oldObject, newObject *corev1.Service) bool {
	return !reflect.DeepEqual(oldObject.Spec, newObject.Spec) ||
		!reflect.DeepEqual(oldObject.Status, newObject.Status) ||
		!reflect.DeepEqual(oldObject.Labels, newObject.Labels) ||
		!reflect.DeepEqual(oldObject.OwnerReferences, newObject.OwnerReferences)
}
