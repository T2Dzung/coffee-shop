package controller

import (
	"context"
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
)

const (
	// AdoptionAnnotationKey is the child-side half of the explicit double
	// opt-in contract (D-6.2-07).
	AdoptionAnnotationKey = "platform.t2dzung.github.io/adopt-by"

	adoptionReasonPolicyNever        = "AdoptionPolicyNever"
	adoptionReasonAnnotationMissing  = "AdoptionAnnotationMissing"
	adoptionReasonAnnotationMismatch = "AdoptionAnnotationMismatch"
	adoptionReasonUnsafeOwnership    = "UnsafeControllerOwnership"
	adoptionReasonIncompatibleIntent = "IncompatibleIntent"
	adoptionStageDryRun              = "DryRun"
	adoptionStageCommit              = "Commit"
)

// AdoptionOutcome records one per-object adoption decision. Adoption is staged:
// at most one existing child is adopted in a reconciliation.
type AdoptionOutcome struct {
	Resource  string
	Attempted bool
	Adopted   bool
	Reason    string
}

// AdoptionError identifies an API failure in the dry-run or commit stage.
type AdoptionError struct {
	Resource string
	Stage    string
	Err      error
}

func (e *AdoptionError) Error() string {
	return fmt.Sprintf("%s adoption %s failed: %v", e.Resource, e.Stage, e.Err)
}

func (e *AdoptionError) Unwrap() error {
	return e.Err
}

func tryAdoptDeployment(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	parent *platformv1alpha1.CoffeeShopService,
	live *appsv1.Deployment,
	desired *appsv1.Deployment,
	ownership OwnershipResult,
) (*AdoptionOutcome, error) {
	outcome := adoptionEligibility(parent, live, "Deployment", ownership)
	if outcome.Reason != "" {
		return outcome, nil
	}
	if err := preflightDeploymentAdoption(parent, live, desired); err != nil {
		outcome.Reason = adoptionReasonIncompatibleIntent + ": " + err.Error()
		return outcome, nil
	}
	if err := applyObject(ctx, c, scheme, parent, desired, client.DryRunAll, client.ForceOwnership); err != nil {
		return outcome, &AdoptionError{Resource: outcome.Resource, Stage: adoptionStageDryRun, Err: err}
	}
	if err := applyObject(ctx, c, scheme, parent, desired, client.ForceOwnership); err != nil {
		return outcome, &AdoptionError{Resource: outcome.Resource, Stage: adoptionStageCommit, Err: err}
	}
	outcome.Adopted = true
	return outcome, nil
}

func tryAdoptService(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	parent *platformv1alpha1.CoffeeShopService,
	live *corev1.Service,
	desired *corev1.Service,
	ownership OwnershipResult,
) (*AdoptionOutcome, error) {
	outcome := adoptionEligibility(parent, live, "Service", ownership)
	if outcome.Reason != "" {
		return outcome, nil
	}
	if err := preflightServiceAdoption(parent, live, desired); err != nil {
		outcome.Reason = adoptionReasonIncompatibleIntent + ": " + err.Error()
		return outcome, nil
	}
	if err := applyObject(ctx, c, scheme, parent, desired, client.DryRunAll, client.ForceOwnership); err != nil {
		return outcome, &AdoptionError{Resource: outcome.Resource, Stage: adoptionStageDryRun, Err: err}
	}
	if err := applyObject(ctx, c, scheme, parent, desired, client.ForceOwnership); err != nil {
		return outcome, &AdoptionError{Resource: outcome.Resource, Stage: adoptionStageCommit, Err: err}
	}
	outcome.Adopted = true
	return outcome, nil
}

func adoptionEligibility(
	parent *platformv1alpha1.CoffeeShopService,
	child client.Object,
	resource string,
	ownership OwnershipResult,
) *AdoptionOutcome {
	outcome := &AdoptionOutcome{Resource: resource}
	if parent.Spec.AdoptionPolicy != platformv1alpha1.AdoptionPolicyExplicit {
		outcome.Reason = adoptionReasonPolicyNever
		return outcome
	}
	outcome.Attempted = true

	if ownership != OwnershipUnownedCollision {
		outcome.Reason = adoptionReasonUnsafeOwnership
		return outcome
	}

	value, found := child.GetAnnotations()[AdoptionAnnotationKey]
	if !found {
		outcome.Reason = adoptionReasonAnnotationMissing
		return outcome
	}
	if value != parent.Name {
		outcome.Reason = adoptionReasonAnnotationMismatch
		return outcome
	}
	return outcome
}

func preflightDeploymentAdoption(
	parent *platformv1alpha1.CoffeeShopService,
	live *appsv1.Deployment,
	desired *appsv1.Deployment,
) error {
	if live.Name != parent.Name || live.Namespace != parent.Namespace {
		return fmt.Errorf("name or namespace does not match parent")
	}
	if !reflect.DeepEqual(live.Spec.Selector, desired.Spec.Selector) {
		return fmt.Errorf("immutable Deployment selector differs from desired")
	}
	for i := range live.Spec.Template.Spec.Containers {
		if live.Spec.Template.Spec.Containers[i].Name == parent.Name {
			return nil
		}
	}
	return fmt.Errorf("main container %q does not exist", parent.Name)
}

func preflightServiceAdoption(
	parent *platformv1alpha1.CoffeeShopService,
	live *corev1.Service,
	desired *corev1.Service,
) error {
	if live.Name != parent.Name || live.Namespace != parent.Namespace {
		return fmt.Errorf("name or namespace does not match parent")
	}
	if live.Spec.Type != corev1.ServiceTypeClusterIP {
		return fmt.Errorf("service type %q is not ClusterIP", live.Spec.Type)
	}
	if live.Spec.ClusterIP == corev1.ClusterIPNone {
		return fmt.Errorf("headless Service identity is incompatible")
	}
	if !reflect.DeepEqual(live.Spec.Selector, desired.Spec.Selector) {
		return fmt.Errorf("service selector differs from desired")
	}
	// Service ports are an associative SSA list. Forcing a differently keyed
	// item can retain the old item and make the request invalid (for example,
	// duplicate port names), so adoption requires the reviewed identity to
	// already match instead of trying to reshape it.
	if !reflect.DeepEqual(live.Spec.Ports, desired.Spec.Ports) {
		return fmt.Errorf("service ports differ from desired")
	}
	return nil
}
