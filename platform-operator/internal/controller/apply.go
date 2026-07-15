package controller

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-operator/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-operator/internal/resource"
)

// FieldManager is the stable SSA field manager used by this operator (D-6.2-01).
const FieldManager = "coffeeshop-operator"

// ApplyResult captures the outcome of applying a child resource.
type ApplyResult struct {
	DeploymentApplied bool
	ServiceApplied    bool
	Adoption          *AdoptionOutcome

	// DeploymentConflict is set when ownership check prevents Deployment mutation.
	DeploymentConflict OwnershipResult
	// ServiceConflict is set when ownership check prevents Service mutation.
	ServiceConflict OwnershipResult
}

// HasConflict returns true if any child was blocked by an ownership conflict.
func (r *ApplyResult) HasConflict() bool {
	return r.DeploymentConflict != "" || r.ServiceConflict != ""
}

// ApplyDesiredChildren creates or reconciles child resources via SSA.
//
// It follows the plan's execution order (Section 7.2):
//  1. Deployment is always in the desired set.
//  2. Service is in the desired set only when spec.service.enabled=true.
//  3. SSA apply Deployment before Service.
//  4. Ownership check happens before every child mutation.
//  5. ForceOwnership is NOT used in steady state (D-6.2-02).
func ApplyDesiredChildren(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	parent *platformv1alpha1.CoffeeShopService,
	obs *Observation,
) (*ApplyResult, error) {
	result := &ApplyResult{}

	// 1. Build desired Deployment
	desiredDeploy, err := resource.BuildDeployment(parent)
	if err != nil {
		return nil, fmt.Errorf("building deployment: %w", err)
	}

	// 2. Apply Deployment
	deployOwnership := ClassifyDeploymentOwnership(parent, obs.Deployment, obs.DeploymentExists)
	switch deployOwnership {
	case OwnershipAbsent, OwnershipOwned:
		if err := applyObject(ctx, c, scheme, parent, desiredDeploy); err != nil {
			return nil, fmt.Errorf("applying deployment: %w", err)
		}
		result.DeploymentApplied = true
	default:
		outcome, adoptErr := tryAdoptDeployment(ctx, c, scheme, parent, obs.Deployment, desiredDeploy, deployOwnership)
		result.Adoption = outcome
		if adoptErr != nil {
			return result, fmt.Errorf("adopting deployment: %w", adoptErr)
		}
		if outcome.Adopted {
			result.DeploymentApplied = true
			// Stage adoption one object per reconciliation. The ownerReference
			// update wakes the controller through .Owns().
			return result, nil
		}
		result.DeploymentConflict = deployOwnership
		return result, nil
	}

	// 3. Build desired Service (may be nil if disabled)
	desiredService, err := resource.BuildService(parent)
	if err != nil {
		return nil, fmt.Errorf("building service: %w", err)
	}

	// 4. Apply or skip Service
	if desiredService != nil {
		svcOwnership := ClassifyServiceOwnership(parent, obs.Service, obs.ServiceExists)
		switch svcOwnership {
		case OwnershipAbsent, OwnershipOwned:
			if err := applyObject(ctx, c, scheme, parent, desiredService); err != nil {
				return nil, fmt.Errorf("applying service: %w", err)
			}
			result.ServiceApplied = true
		default:
			outcome, adoptErr := tryAdoptService(ctx, c, scheme, parent, obs.Service, desiredService, svcOwnership)
			result.Adoption = outcome
			if adoptErr != nil {
				return result, fmt.Errorf("adopting service: %w", adoptErr)
			}
			if outcome.Adopted {
				result.ServiceApplied = true
				return result, nil
			}
			result.ServiceConflict = svcOwnership
		}
	}

	return result, nil
}

// applyObject performs SSA (Server-Side Apply) on a child resource with the
// operator's stable FieldManager and controller ownerReference.
func applyObject(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	parent *platformv1alpha1.CoffeeShopService,
	obj client.Object,
	options ...client.ApplyOption,
) error {
	// Set controller ownerReference
	ownerRef := DesiredOwnerReference(parent)
	obj.SetOwnerReferences([]metav1.OwnerReference{ownerRef})

	// Ensure GVK is set (required for SSA)
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("looking up GVK for %T: %w", obj, err)
	}
	if len(gvks) > 0 {
		obj.GetObjectKind().SetGroupVersionKind(gvks[0])
	}

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("converting %T to unstructured apply configuration: %w", obj, err)
	}
	applyObject := &unstructured.Unstructured{Object: content}
	applyObject.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

	applyOptions := make([]client.ApplyOption, 0, 1+len(options))
	applyOptions = append(applyOptions, client.FieldOwner(FieldManager))
	applyOptions = append(applyOptions, options...)
	operation := writeOperationApply
	parsedOptions := (&client.ApplyOptions{}).ApplyOptions(applyOptions)
	if slices.Contains(parsedOptions.DryRun, metav1.DryRunAll) {
		operation = writeOperationApplyDryRun
	}

	resourceLabel := writeResourceDeployment
	if _, ok := obj.(*corev1.Service); ok {
		resourceLabel = writeResourceService
	}
	err = c.Apply(ctx, client.ApplyConfigurationFromUnstructured(applyObject), applyOptions...)
	recordWrite(operation, resourceLabel, err)
	return err
}

// DeleteOwnedService deletes a Service only if it is owned by the parent (D-6.2-11).
// Returns (deleted bool, conflict OwnershipResult, err error).
func DeleteOwnedService(
	ctx context.Context,
	c client.Client,
	parent *platformv1alpha1.CoffeeShopService,
	obs *Observation,
) (bool, OwnershipResult, error) {
	if !obs.ServiceExists || obs.Service == nil {
		return false, "", nil
	}

	ownership := ClassifyServiceOwnership(parent, obs.Service, obs.ServiceExists)
	switch ownership {
	case OwnershipOwned:
		if err := c.Delete(ctx, obs.Service); err != nil {
			recordWrite(writeOperationDelete, writeResourceService, err)
			return false, "", fmt.Errorf("deleting owned service: %w", err)
		}
		recordWrite(writeOperationDelete, writeResourceService, nil)
		return true, "", nil
	default:
		// Unowned, foreign, stale — do NOT delete (D-6.2-11)
		return false, ownership, nil
	}
}
