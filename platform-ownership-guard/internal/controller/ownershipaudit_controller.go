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
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/telemetry"
)

// OwnershipAuditReconciler owns only OwnershipAudit status writes.
type OwnershipAuditReconciler struct {
	Reader        client.Reader
	StatusWriter  client.SubResourceWriter
	Collector     inventory.InventoryCollector
	Evaluator     FoundationEvaluator
	Scheme        *runtime.Scheme
	StatusBuilder *StatusBuilder
	Recorder      EventRecorder
	Telemetry     TelemetryRecorder
	Jitter        JitterFunc
	Now           func() time.Time
}

// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits,verbs=get;list;watch
// +kubebuilder:rbac:groups=guard.platform.t2dzung.github.io,resources=ownershipaudits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch;update

func (r *OwnershipAuditReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	if r.Reader == nil || r.Collector == nil || r.StatusWriter == nil {
		if r.Telemetry != nil {
			r.Telemetry.RecordScan(telemetry.ResultTerminalError, time.Since(startTime))
		}
		return ctrl.Result{}, fmt.Errorf("reconciler dependencies Reader, Collector, and StatusWriter are required")
	}

	audit := &guardplatformv1alpha1.OwnershipAudit{}
	if err := r.Reader.Get(ctx, req.NamespacedName, audit); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		if r.Telemetry != nil {
			r.Telemetry.RecordScan(telemetry.ResultAuditReadError, time.Since(startTime))
		}
		return ctrl.Result{}, err
	}

	now := r.Now
	if now == nil {
		now = time.Now
	}
	builder := r.StatusBuilder
	if builder == nil {
		builder = NewStatusBuilder(now)
	}
	evaluator := r.Evaluator
	if evaluator == nil {
		evaluator = NoopFoundationEvaluator{}
	}

	// Read old persisted findings for transition calculation
	oldFindings := make([]guardplatformv1alpha1.OwnershipFinding, len(audit.Status.Findings))
	copy(oldFindings, audit.Status.Findings)

	snapshot, collectErr := r.Collector.Collect(ctx, audit.Namespace, &audit.Spec)
	if r.Telemetry != nil {
		r.Telemetry.RecordSnapshotErrors(snapshot, collectErr)
	}

	var overrideReason, overrideMessage string
	var transientErr error
	var isTerminalCollectionErr bool

	if collectErr != nil {
		if typed, ok := collectErr.(*inventory.InventoryError); ok {
			overrideReason = string(typed.DTO.Class)
			overrideMessage = typed.DTO.Message
			if typed.DTO.Class == inventory.ErrTransientReadFailure {
				transientErr = collectErr
			} else {
				isTerminalCollectionErr = true
			}
		} else {
			overrideReason = string(inventory.ErrTransientReadFailure)
			overrideMessage = collectErr.Error()
			transientErr = collectErr
		}
	}

	findings := evaluator.Evaluate(snapshot, audit.Spec.Detectors)
	desired := builder.BuildStatus(
		&audit.Status,
		audit.Generation,
		snapshot,
		findings,
		audit.Spec.ResyncInterval.Duration,
		overrideReason,
		overrideMessage,
	)

	// Determine if inventory is healthy enough to issue Resolved transitions
	inventoryReadyCond := meta.FindStatusCondition(desired.Conditions, "InventoryReady")
	inventoryReady := inventoryReadyCond != nil && inventoryReadyCond.Status == metav1.ConditionTrue

	// Compute finding transitions between old persisted status and desired status
	transitions := ComputeFindingTransitions(oldFindings, desired.Findings, inventoryReady)

	// Status-first: patch status subresource before publishing Events/transition metrics
	if !SemanticEqualStatus(&audit.Status, desired) || heartbeatChanged(audit.Status.LastCompletedScanTime, desired.LastCompletedScanTime) {
		if err := r.patchStatus(ctx, audit, desired); err != nil {
			log.FromContext(ctx).Error(err, "Failed to patch OwnershipAudit status")
			if r.Telemetry != nil {
				r.Telemetry.RecordScan(telemetry.ResultStatusWriteError, time.Since(startTime))
			}
			return ctrl.Result{}, err
		}
	}

	// Status patch succeeded: publish transition Events and metrics
	r.publishEventsAndMetrics(audit, transitions)

	// Determine telemetry outcome and resync requeue
	scanOutcome := telemetry.ResultSuccess
	if !inventoryReady {
		if inventoryReadyCond != nil && inventoryReadyCond.Reason == string(inventory.ErrTransientReadFailure) {
			scanOutcome = telemetry.ResultTransientError
		} else {
			scanOutcome = telemetry.ResultTerminalError
		}
	} else if isTerminalCollectionErr {
		scanOutcome = telemetry.ResultTerminalError
	} else if transientErr != nil {
		scanOutcome = telemetry.ResultTransientError
	}

	duration := time.Since(startTime)
	if r.Telemetry != nil {
		r.Telemetry.RecordScan(scanOutcome, duration)
	}

	// Log structured debug summary
	logger := log.FromContext(ctx)
	if logger.V(1).Enabled() {
		addedCount, changedCount, resolvedCount := countTransitions(transitions)
		logger.V(1).Info("Audit reconcile completed",
			"audit_namespace", audit.Namespace,
			"audit_name", audit.Name,
			"result", scanOutcome,
			"findings_count", len(desired.Findings),
			"added_count", addedCount,
			"changed_count", changedCount,
			"resolved_count", resolvedCount,
			"duration_ms", duration.Milliseconds(),
		)
	}

	if transientErr != nil {
		return ctrl.Result{}, transientErr
	}

	resyncBase := audit.Spec.ResyncInterval.Duration
	if resyncBase <= 0 {
		resyncBase = 10 * time.Minute
	}
	jitterFunc := r.Jitter
	if jitterFunc == nil {
		jitterFunc = DefaultJitter
	}

	return ctrl.Result{RequeueAfter: jitterFunc(resyncBase)}, nil
}

func (r *OwnershipAuditReconciler) publishEventsAndMetrics(audit *guardplatformv1alpha1.OwnershipAudit, transitions []FindingTransition) {
	for _, tr := range transitions {
		if tr.Type == TransitionUnchanged {
			continue
		}

		if r.Telemetry != nil {
			r.Telemetry.RecordTransition(tr.Finding.Detector, tr.Finding.Severity, tr.Finding.Confidence, string(tr.Type))
		}

		if r.Recorder != nil {
			eventType := "Normal"
			if tr.Finding.Severity == guardplatformv1alpha1.SeverityWarning || tr.Finding.Severity == guardplatformv1alpha1.SeverityHigh {
				if tr.Type == TransitionAdded || tr.Type == TransitionChanged {
					eventType = "Warning"
				}
			}

			reason := "FindingDetected"
			if tr.Type == TransitionChanged {
				reason = "FindingChanged"
			} else if tr.Type == TransitionResolved {
				reason = "FindingResolved"
			}

			targetRef := fmt.Sprintf("%s/%s/%s/%s/%s",
				tr.Finding.Target.APIGroup,
				tr.Finding.Target.Version,
				tr.Finding.Target.Kind,
				tr.Finding.Target.Namespace,
				tr.Finding.Target.Name,
			)

			note := fmt.Sprintf("detector=%s target=%s severity=%s confidence=%s id=%s",
				tr.Finding.Detector, targetRef, tr.Finding.Severity, tr.Finding.Confidence, tr.Finding.ID)
			if len(note) > 512 {
				note = note[:512]
			}

			r.Recorder.Eventf(audit, nil, eventType, reason, "AuditFinding", "%s", note)
		}
	}
}

func countTransitions(transitions []FindingTransition) (added, changed, resolved int) {
	for _, tr := range transitions {
		switch tr.Type {
		case TransitionAdded:
			added++
		case TransitionChanged:
			changed++
		case TransitionResolved:
			resolved++
		}
	}
	return
}

// patchStatus uses the object read for evaluation as the merge base.
// A conflict is returned so the next workqueue attempt re-reads and re-evaluates.
func (r *OwnershipAuditReconciler) patchStatus(
	ctx context.Context,
	audit *guardplatformv1alpha1.OwnershipAudit,
	desired *guardplatformv1alpha1.OwnershipAuditStatus,
) error {
	base := audit.DeepCopy()
	audit.Status = *desired.DeepCopy()
	return r.StatusWriter.Patch(ctx, audit, client.MergeFrom(base))
}

func heartbeatChanged(oldTime, newTime *metav1.Time) bool {
	if oldTime == nil || newTime == nil {
		return oldTime != nil || newTime != nil
	}
	return !oldTime.Equal(newTime)
}

func (r *OwnershipAuditReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guardplatformv1alpha1.OwnershipAudit{}).
		Named("ownershipaudit").
		Complete(r)
}
