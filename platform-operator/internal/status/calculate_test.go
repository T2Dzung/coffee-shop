package status

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

func makeService(replicas int32, serviceEnabled bool) *platformv1alpha1.CoffeeShopService {
	svc := &platformv1alpha1.CoffeeShopService{
		ObjectMeta: metav1.ObjectMeta{Generation: 3},
		Spec: platformv1alpha1.CoffeeShopServiceSpec{
			ManagementPolicy: platformv1alpha1.ManagementPolicyObserve,
			Replicas:         replicas,
		},
	}
	if serviceEnabled {
		svc.Spec.Service = &platformv1alpha1.ServiceSpec{Enabled: true}
	}
	return svc
}

func findCondition(delta *StatusDelta, condType string) *ConditionEntry {
	for i := range delta.Conditions {
		if delta.Conditions[i].Type == condType {
			return &delta.Conditions[i]
		}
	}
	return nil
}

func TestCalculateObserveStatus_MissingChildren(t *testing.T) {
	svc := makeService(2, true)
	obs := &ObservationInput{
		ServiceEnabled: true,
	}

	delta := CalculateObserveStatus(svc, obs)

	if delta.ObservedGeneration != 3 {
		t.Errorf("ObservedGeneration = %d, want 3", delta.ObservedGeneration)
	}
	if delta.DesiredReplicas != 2 {
		t.Errorf("DesiredReplicas = %d, want 2", delta.DesiredReplicas)
	}
	if delta.ReadyReplicas != 0 {
		t.Errorf("ReadyReplicas = %d, want 0", delta.ReadyReplicas)
	}

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "ObserveOnly" {
		t.Errorf("Ready condition = %+v, want False/ObserveOnly", ready)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != "WorkloadMissing" {
		t.Errorf("WorkloadReady condition = %+v, want False/WorkloadMissing", workload)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionFalse || guardrails.Reason != "ServiceMissing" {
		t.Errorf("GuardrailsReady condition = %+v, want False/ServiceMissing", guardrails)
	}
}

func TestCalculateObserveStatus_AvailableWorkload(t *testing.T) {
	svc := makeService(2, true)
	obs := &ObservationInput{
		DeploymentExists: true,
		LiveDeployment: &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		ServiceEnabled: true,
		ServiceExists:  true,
	}

	delta := CalculateObserveStatus(svc, obs)

	if delta.ReadyReplicas != 2 {
		t.Errorf("ReadyReplicas = %d, want 2", delta.ReadyReplicas)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionTrue || workload.Reason != "WorkloadAvailable" {
		t.Errorf("WorkloadReady = %+v, want True/WorkloadAvailable", workload)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionTrue || guardrails.Reason != "ServiceAvailable" {
		t.Errorf("GuardrailsReady = %+v, want True/ServiceAvailable", guardrails)
	}
}

func TestCalculateObserveStatus_DriftedDeployment(t *testing.T) {
	svc := makeService(2, false)
	obs := &ObservationInput{
		DeploymentExists:  true,
		DeploymentDrifted: true,
		LiveDeployment: &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
	}

	delta := CalculateObserveStatus(svc, obs)

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != "WorkloadDrifted" {
		t.Errorf("WorkloadReady = %+v, want False/WorkloadDrifted", workload)
	}
}

func TestCalculateObserveStatus_UnavailableReplicas(t *testing.T) {
	svc := makeService(3, false)
	obs := &ObservationInput{
		DeploymentExists: true,
		LiveDeployment: &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{AvailableReplicas: 1},
		},
	}

	delta := CalculateObserveStatus(svc, obs)

	if delta.ReadyReplicas != 1 {
		t.Errorf("ReadyReplicas = %d, want 1", delta.ReadyReplicas)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != ReasonWorkloadUnavailable {
		t.Errorf("WorkloadReady = %+v, want False/WorkloadUnavailable", workload)
	}
}

func TestCalculateObserveStatus_ServiceDisabled(t *testing.T) {
	svc := makeService(2, false)
	obs := &ObservationInput{
		DeploymentExists: true,
		LiveDeployment: &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		ServiceEnabled: false,
	}

	delta := CalculateObserveStatus(svc, obs)

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionTrue || guardrails.Reason != "ServiceDisabled" {
		t.Errorf("GuardrailsReady = %+v, want True/ServiceDisabled", guardrails)
	}
}

func TestCalculateObserveStatus_ServiceDrifted(t *testing.T) {
	svc := makeService(2, true)
	obs := &ObservationInput{
		DeploymentExists: true,
		LiveDeployment: &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
		},
		ServiceEnabled: true,
		ServiceExists:  true,
		ServiceDrifted: true,
	}

	delta := CalculateObserveStatus(svc, obs)

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionFalse || guardrails.Reason != "ServiceDrifted" {
		t.Errorf("GuardrailsReady = %+v, want False/ServiceDrifted", guardrails)
	}
}

func TestCalculateInvalidSpecStatus(t *testing.T) {
	svc := makeService(2, true)
	delta := CalculateInvalidSpecStatus(svc, "image tag missing")

	if delta.ReadyReplicas != 0 {
		t.Errorf("ReadyReplicas = %d, want 0", delta.ReadyReplicas)
	}

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonInvalidSpec {
		t.Errorf("Ready = %+v, want False/InvalidSpec", ready)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != ReasonInvalidSpec {
		t.Errorf("WorkloadReady = %+v, want False/InvalidSpec", workload)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionFalse || guardrails.Reason != ReasonInvalidSpec {
		t.Errorf("GuardrailsReady = %+v, want False/InvalidSpec", guardrails)
	}
}

func TestCalculateManageStatus_Success(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment: &appsv1.Deployment{
				Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
			},
			ServiceEnabled: true,
			ServiceExists:  true,
		},
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != "Reconciled" {
		t.Errorf("Ready condition = %+v, want True/Reconciled", ready)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionTrue || workload.Reason != "WorkloadAvailable" {
		t.Errorf("WorkloadReady = %+v, want True/WorkloadAvailable", workload)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionTrue || guardrails.Reason != "ServiceAvailable" {
		t.Errorf("GuardrailsReady = %+v, want True/ServiceAvailable", guardrails)
	}
}

func TestCalculateManageStatus_AppliedButNotObservedIsUnavailable(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment:   nil,
			ServiceEnabled:   true,
			ServiceExists:    true,
		},
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonWorkloadUnavailable {
		t.Errorf("Ready condition = %+v, want False/WorkloadUnavailable", ready)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != ReasonWorkloadUnavailable {
		t.Errorf("WorkloadReady = %+v, want False/WorkloadUnavailable", workload)
	}
}

func TestCalculateManageStatus_OwnershipConflictDeployment(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment:   &appsv1.Deployment{},
			ServiceEnabled:   true,
			ServiceExists:    true,
		},
		DeploymentConflict: OwnershipUnownedCollision,
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonOwnershipConflict {
		t.Errorf("Ready condition = %+v, want False/OwnershipConflict", ready)
	}

	workload := findCondition(delta, ConditionWorkloadReady)
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != ReasonOwnershipConflict {
		t.Errorf("WorkloadReady = %+v, want False/OwnershipConflict", workload)
	}
}

func TestCalculateManageStatus_OwnershipConflictService(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment: &appsv1.Deployment{
				Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
			},
			ServiceEnabled: true,
			ServiceExists:  true,
		},
		ServiceConflict: OwnershipForeignOwnedCollision,
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonOwnershipConflict {
		t.Errorf("Ready condition = %+v, want False/OwnershipConflict", ready)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionFalse || guardrails.Reason != ReasonOwnershipConflict {
		t.Errorf("GuardrailsReady = %+v, want False/OwnershipConflict", guardrails)
	}
}

func TestCalculateManageStatus_PruneConflictService(t *testing.T) {
	svc := makeService(2, false) // Service disabled in spec
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment: &appsv1.Deployment{
				Status: appsv1.DeploymentStatus{AvailableReplicas: 2},
			},
			ServiceEnabled: false,
			ServiceExists:  true, // but service still exists in cluster
		},
		PruneConflict: OwnershipUnownedCollision,
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonOwnershipConflict {
		t.Errorf("Ready condition = %+v, want False/OwnershipConflict", ready)
	}

	guardrails := findCondition(delta, ConditionGuardrailsReady)
	if guardrails == nil || guardrails.Status != metav1.ConditionFalse || guardrails.Reason != ReasonOwnershipConflict {
		t.Errorf("GuardrailsReady = %+v, want False/OwnershipConflict", guardrails)
	}
}

func TestCalculateManageStatus_ApplyConflictError(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment:   &appsv1.Deployment{},
			ServiceEnabled:   true,
		},
		ApplyError:       "apply conflict on fields replicas",
		ApplyErrorReason: ReasonApplyConflict,
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonApplyConflict {
		t.Errorf("Ready condition = %+v, want False/ApplyConflict", ready)
	}
}

func TestCalculateManageStatus_GenericApplyFailure(t *testing.T) {
	svc := makeService(2, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment:   &appsv1.Deployment{},
			ServiceEnabled:   true,
		},
		ApplyError:       "api server timeout",
		ApplyErrorReason: ReasonApplyFailed,
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonApplyFailed {
		t.Errorf("Ready condition = %+v, want False/ApplyFailed", ready)
	}
}

func TestCalculateManageStatus_WorkloadUnavailable(t *testing.T) {
	svc := makeService(3, true)
	input := &ManageInput{
		Obs: &ObservationInput{
			DeploymentExists: true,
			LiveDeployment: &appsv1.Deployment{
				Status: appsv1.DeploymentStatus{AvailableReplicas: 1}, // 1 < 3
			},
			ServiceEnabled: true,
			ServiceExists:  true,
		},
	}

	delta := CalculateManageStatus(svc, input)

	ready := findCondition(delta, ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonWorkloadUnavailable {
		t.Errorf("Ready condition = %+v, want False/WorkloadUnavailable", ready)
	}
}
