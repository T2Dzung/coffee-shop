package status

import (
	"reflect"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

const (
	ConditionReady            = "Ready"
	ConditionWorkloadReady    = "WorkloadReady"
	ConditionGuardrailsReady  = "GuardrailsReady"
	ConditionPolicyConfigured = "PolicyConfigured"

	ReasonInvalidSpec         = "InvalidSpec"
	ReasonOwnershipConflict   = "OwnershipConflict"
	ReasonApplyConflict       = "ApplyConflict"
	ReasonApplyFailed         = "ApplyFailed"
	ReasonWorkloadUnavailable = "WorkloadUnavailable"
)

// SetCondition applies Kubernetes condition semantics and reports whether status
// changed. The caller supplies time so unit tests remain deterministic.
func SetCondition(
	service *platformv1alpha1.CoffeeShopService,
	conditionType string,
	conditionStatus metav1.ConditionStatus,
	reason string,
	message string,
	transitionTime metav1.Time,
) bool {
	before := append([]metav1.Condition(nil), service.Status.Conditions...)
	apimeta.SetStatusCondition(&service.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             conditionStatus,
		ObservedGeneration: service.Generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: transitionTime,
	})
	return !reflect.DeepEqual(before, service.Status.Conditions)
}
