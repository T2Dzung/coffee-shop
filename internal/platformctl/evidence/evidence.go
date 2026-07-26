package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	Time     time.Time      `json:"time"`
	Phase    string         `json:"phase"`
	Step     string         `json:"step"`
	Status   string         `json:"status"`
	Duration time.Duration  `json:"duration,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type Bundle struct {
	SchemaVersion int       `json:"schema_version"`
	Operation     string    `json:"operation"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Status        string    `json:"status"`
	Events        []Event   `json:"events"`
}

type Recorder struct {
	mu     sync.Mutex
	bundle Bundle
	clock  func() time.Time
}

func New(operation string) *Recorder {
	now := time.Now
	return &Recorder{
		clock: now,
		bundle: Bundle{
			SchemaVersion: 1,
			Operation:     operation,
			StartedAt:     now().UTC(),
			Status:        "running",
		},
	}
}

func (r *Recorder) Record(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Time.IsZero() {
		event.Time = r.clock().UTC()
	}
	r.bundle.Events = append(r.bundle.Events, event)
}

func (r *Recorder) Finish(status string) Bundle {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bundle.Status = status
	r.bundle.FinishedAt = r.clock().UTC()
	return clone(r.bundle)
}

func (r *Recorder) Snapshot() Bundle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return clone(r.bundle)
}

func WriteAtomic(path string, bundle Bundle) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".evidence-*.json")
	if err != nil {
		return fmt.Errorf("create evidence temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func clone(value Bundle) Bundle {
	result := value
	result.Events = append([]Event(nil), value.Events...)
	return result
}
