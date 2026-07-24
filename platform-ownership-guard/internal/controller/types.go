package controller

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	guardplatformv1alpha1 "github.com/T2Dzung/coffee-shop/platform-ownership-guard/api/v1alpha1"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/telemetry"
)

// EventRecorder defines a narrow Events v1 recording interface.
type EventRecorder interface {
	Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{})
}

// TelemetryRecorder abstracts Prometheus metrics recording.
type TelemetryRecorder interface {
	RecordScan(result string, duration time.Duration)
	RecordInventoryError(source, reason string)
	RecordTransition(detector guardplatformv1alpha1.DetectorType, severity guardplatformv1alpha1.FindingSeverity, confidence guardplatformv1alpha1.FindingConfidence, transition string)
	RecordSnapshotErrors(snapshot *inventory.NormalizedSnapshot, collectErr error)
}

// JitterFunc abstracts duration jittering.
type JitterFunc func(base time.Duration) time.Duration

// DefaultJitter implements 10% additive random jitter clamped to [base, 24h].
func DefaultJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	maxInterval := 24 * time.Hour
	if base >= maxInterval {
		return maxInterval
	}
	// wait.Jitter(base, 0.10) returns base + rand(0, 0.10 * base)
	jittered := wait.Jitter(base, 0.10)
	if jittered > maxInterval {
		return maxInterval
	}
	return jittered
}

// TelemetryWrapper bridges internal/telemetry.Metrics to TelemetryRecorder.
type TelemetryWrapper struct {
	Metrics *telemetry.Metrics
}

func (w *TelemetryWrapper) RecordScan(result string, duration time.Duration) {
	if w != nil && w.Metrics != nil {
		w.Metrics.RecordScan(result, duration)
	}
}

func (w *TelemetryWrapper) RecordInventoryError(source, reason string) {
	if w != nil && w.Metrics != nil {
		w.Metrics.RecordInventoryError(source, reason)
	}
}

func (w *TelemetryWrapper) RecordTransition(detector guardplatformv1alpha1.DetectorType, severity guardplatformv1alpha1.FindingSeverity, confidence guardplatformv1alpha1.FindingConfidence, transition string) {
	if w != nil && w.Metrics != nil {
		w.Metrics.RecordTransition(detector, severity, confidence, transition)
	}
}

func (w *TelemetryWrapper) RecordSnapshotErrors(snapshot *inventory.NormalizedSnapshot, collectErr error) {
	if w != nil && w.Metrics != nil {
		telemetry.RecordSnapshotInventoryErrors(w.Metrics, snapshot, collectErr)
	}
}
