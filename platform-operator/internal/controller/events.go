package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
)

// eventRecorder keeps controller call sites and existing fake recorders small
// while the manager uses the current events.k8s.io recorder API.
type eventRecorder interface {
	Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...any)
}

type eventRecorderAdapter struct {
	recorder events.EventRecorder
}

func (a eventRecorderAdapter) Eventf(
	object runtime.Object,
	eventType string,
	reason string,
	messageFmt string,
	args ...any,
) {
	a.recorder.Eventf(object, nil, eventType, reason, reason, messageFmt, args...)
}
