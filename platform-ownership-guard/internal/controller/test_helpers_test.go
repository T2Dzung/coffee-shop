package controller

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

func IdentityJitter(base time.Duration) time.Duration {
	return base
}

type RecordedEvent struct {
	Regarding runtime.Object
	Related   runtime.Object
	Type      string
	Reason    string
	Action    string
	Note      string
}

type FakeEventRecorder struct {
	mu     sync.Mutex
	Events []RecordedEvent
}

func (f *FakeEventRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Events = append(f.Events, RecordedEvent{
		Regarding: regarding,
		Related:   related,
		Type:      eventtype,
		Reason:    reason,
		Action:    action,
		Note:      fmt.Sprintf(note, args...),
	})
}

func (f *FakeEventRecorder) GetEvents() []RecordedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]RecordedEvent, len(f.Events))
	copy(copied, f.Events)
	return copied
}
