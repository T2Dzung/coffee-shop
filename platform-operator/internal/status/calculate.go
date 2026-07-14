package status

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

// OwnershipResult classifies a live child resource from the operator's perspective.
type OwnershipResult string

const (
	// OwnershipAbsent means the child does not exist.
	OwnershipAbsent OwnershipResult = "Absent"
	// OwnershipOwned means the child has a controller ownerReference pointing to
	// this CR with the correct UID.
	OwnershipOwned OwnershipResult = "Owned"
	// OwnershipUnownedCollision means the child exists but has no controller
	// ownerReference.
	OwnershipUnownedCollision OwnershipResult = "UnownedCollision"
	// OwnershipForeignOwnedCollision means the child has a controller
	// ownerReference from a different controller/owner.
	OwnershipForeignOwnedCollision OwnershipResult = "ForeignOwnedCollision"
	// OwnershipStaleOwnerCollision means the child has a controller ownerReference
	// with the same name but a different (stale) UID.
	OwnershipStaleOwnerCollision OwnershipResult = "StaleOwnerCollision"
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
		ConditionEntry{Type: ConditionReady, Status: metav1.ConditionFalse, Reason: ReasonInvalidSpec, Message: errMsg},
		ConditionEntry{Type: ConditionWorkloadReady, Status: metav1.ConditionFalse, Reason: ReasonInvalidSpec, Message: "Spec validation failed"},
		ConditionEntry{Type: ConditionGuardrailsReady, Status: metav1.ConditionFalse, Reason: ReasonInvalidSpec, Message: "Spec validation failed"},
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
	// An apply success proves that the API object was accepted, not that the
	// Deployment has available Pods. Manage mode can optimistically mark the
	// object as existing before a post-apply GET, so nil live state must remain
	// unavailable instead of falling through to WorkloadAvailable.
	if obs.LiveDeployment == nil || obs.LiveDeployment.Status.AvailableReplicas < service.Spec.Replicas {
		return ConditionEntry{
			Type:    ConditionWorkloadReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonWorkloadUnavailable,
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

// ManageInput provides the outcome of the reconciliation actions in Manage mode.
type ManageInput struct {
	Obs *ObservationInput

	// Ownership conflicts detected during reconciliation.
	DeploymentConflict OwnershipResult
	ServiceConflict    OwnershipResult
	PruneConflict      OwnershipResult

	// Transient or permanent apply errors.
	ApplyError string
}

// CalculateManageStatus computes the status delta in Manage mode.
func CalculateManageStatus(service *platformv1alpha1.CoffeeShopService, input *ManageInput) *StatusDelta {
	delta := &StatusDelta{
		ObservedGeneration: service.Generation,
		DesiredReplicas:    service.Spec.Replicas,
	}

	// ReadyReplicas
	if input.Obs.DeploymentExists && input.Obs.LiveDeployment != nil {
		delta.ReadyReplicas = input.Obs.LiveDeployment.Status.AvailableReplicas
	}

	// Calculate sub-conditions
	workloadCond := calculateWorkloadCondition(service, input.Obs)
	guardrailsCond := calculateGuardrailsCondition(input.Obs)

	// Override GuardrailsReady if service pruning failed due to ownership conflict
	if input.PruneConflict != "" {
		guardrailsCond = ConditionEntry{
			Type:    ConditionGuardrailsReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonOwnershipConflict,
			Message: fmt.Sprintf("Service delete blocked: collision type %s", input.PruneConflict),
		}
	} else if input.ServiceConflict != "" {
		guardrailsCond = ConditionEntry{
			Type:    ConditionGuardrailsReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonOwnershipConflict,
			Message: fmt.Sprintf("Service apply blocked: collision type %s", input.ServiceConflict),
		}
	}

	// Override WorkloadReady if deployment apply failed due to ownership conflict
	if input.DeploymentConflict != "" {
		workloadCond = ConditionEntry{
			Type:    ConditionWorkloadReady,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonOwnershipConflict,
			Message: fmt.Sprintf("Deployment apply blocked: collision type %s", input.DeploymentConflict),
		}
	}

	// Calculate aggregate Ready condition
	readyCond := ConditionEntry{
		Type: ConditionReady,
	}

	switch {
	case input.DeploymentConflict != "" || input.ServiceConflict != "" || input.PruneConflict != "":
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = ReasonOwnershipConflict
		readyCond.Message = "One or more child resources are blocked by ownership conflicts"
	case input.ApplyError != "":
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = "ApplyConflict"
		readyCond.Message = input.ApplyError
	case workloadCond.Status == metav1.ConditionFalse:
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = workloadCond.Reason
		readyCond.Message = workloadCond.Message
	case guardrailsCond.Status == metav1.ConditionFalse:
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = guardrailsCond.Reason
		readyCond.Message = guardrailsCond.Message
	default:
		readyCond.Status = metav1.ConditionTrue
		readyCond.Reason = "Reconciled"
		readyCond.Message = "Workload and service are fully reconciled and healthy"
	}

	delta.Conditions = append(delta.Conditions, readyCond, workloadCond, guardrailsCond)
	return delta
}
