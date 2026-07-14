package status

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// ObservationInput provides the data needed to calculate semantic status
// in Observe mode. This struct decouples status calculation from controller
// internals and API client, making it independently unit-testable.
type ObservationInput struct {
	DeploymentExists  bool
	DeploymentDrifted bool
	ServiceExists     bool
	ServiceDrifted    bool

	// LiveDeployment is set only when DeploymentExists == true.
	LiveDeployment *appsv1.Deployment

	// LiveService is set only when ServiceExists == true.
	LiveService *corev1.Service

	// ServiceEnabled reflects spec.service.enabled (or false when spec.service is nil).
	ServiceEnabled bool
}

// StatusDelta describes every field the controller should patch on the parent
// CoffeeShopService status. It is pure data — no API calls.
type StatusDelta struct {
	ObservedGeneration int64
	DesiredReplicas    int32
	ReadyReplicas      int32
	Conditions         []ConditionEntry
}

// ConditionEntry is a single condition to set via SetCondition.
type ConditionEntry struct {
	Type    string
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

// CalculateObserveStatus computes the semantic status for a CoffeeShopService
// in Observe mode, given observation data and the CR spec.
// It never touches the API server — it only returns pure data for the caller to patch.
func CalculateObserveStatus(service *platformv1alpha1.CoffeeShopService, obs *ObservationInput) *StatusDelta {
	delta := &StatusDelta{
		ObservedGeneration: service.Generation,
		DesiredReplicas:    service.Spec.Replicas,
	}

	// ReadyReplicas
	if obs.DeploymentExists && obs.LiveDeployment != nil {
		delta.ReadyReplicas = obs.LiveDeployment.Status.AvailableReplicas
	}

	// Ready = False / ObserveOnly — always, regardless of workload state
	delta.Conditions = append(delta.Conditions, ConditionEntry{
		Type:    ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "ObserveOnly",
		Message: "Controller is in Observe-only mode; child resources will not be mutated",
	})

	// WorkloadReady
	delta.Conditions = append(delta.Conditions, calculateWorkloadCondition(service, obs))

	// GuardrailsReady
	delta.Conditions = append(delta.Conditions, calculateGuardrailsCondition(obs))

	return delta
}

// CalculateInvalidSpecStatus computes the status delta for an invalid spec.
func CalculateInvalidSpecStatus(service *platformv1alpha1.CoffeeShopService, errMsg string) *StatusDelta {
	delta := &StatusDelta{
		ObservedGeneration: service.Generation,
		DesiredReplicas:    service.Spec.Replicas,
		ReadyReplicas:      0,
	}

	delta.Conditions = append(delta.Conditions,
		ConditionEntry{Type: ConditionReady, Status: metav1.ConditionFalse, Reason: "InvalidSpec", Message: errMsg},
		ConditionEntry{Type: ConditionWorkloadReady, Status: metav1.ConditionFalse, Reason: "InvalidSpec", Message: "Spec validation failed"},
		ConditionEntry{Type: ConditionGuardrailsReady, Status: metav1.ConditionFalse, Reason: "InvalidSpec", Message: "Spec validation failed"},
	)

	return delta
}

func calculateWorkloadCondition(service *platformv1alpha1.CoffeeShopService, obs *ObservationInput) ConditionEntry {
	if !obs.DeploymentExists {
		return ConditionEntry{
			Type:    ConditionWorkloadReady,
			Status:  metav1.ConditionFalse,
			Reason:  "WorkloadMissing",
			Message: "Deployment not found",
		}
	}
	if obs.DeploymentDrifted {
		return ConditionEntry{
			Type:    ConditionWorkloadReady,
			Status:  metav1.ConditionFalse,
			Reason:  "WorkloadDrifted",
			Message: "Deployment exists but has drifted from desired configuration",
		}
	}
	if obs.LiveDeployment != nil && obs.LiveDeployment.Status.AvailableReplicas < service.Spec.Replicas {
		return ConditionEntry{
			Type:    ConditionWorkloadReady,
			Status:  metav1.ConditionFalse,
			Reason:  "WorkloadUnavailable",
			Message: "Deployment replicas are not fully available",
		}
	}
	return ConditionEntry{
		Type:    ConditionWorkloadReady,
		Status:  metav1.ConditionTrue,
		Reason:  "WorkloadAvailable",
		Message: "Deployment is available with required replicas",
	}
}

func calculateGuardrailsCondition(obs *ObservationInput) ConditionEntry {
	if !obs.ServiceEnabled {
		return ConditionEntry{
			Type:    ConditionGuardrailsReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ServiceDisabled",
			Message: "Service creation is disabled",
		}
	}
	if !obs.ServiceExists {
		return ConditionEntry{
			Type:    ConditionGuardrailsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ServiceMissing",
			Message: "Service not found",
		}
	}
	if obs.ServiceDrifted {
		return ConditionEntry{
			Type:    ConditionGuardrailsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "ServiceDrifted",
			Message: "Service has drifted from desired configuration",
		}
	}
	return ConditionEntry{
		Type:    ConditionGuardrailsReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ServiceAvailable",
		Message: "Service is available and matches configuration",
	}
}
