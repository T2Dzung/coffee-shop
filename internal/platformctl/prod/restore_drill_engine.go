package prod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
)

const restoreDrillStateSchemaVersion = 1

type RestoreDrillPhase string

const (
	RestoreDrillInitialized           RestoreDrillPhase = "initialized"
	RestoreDrillMarkerAWritten        RestoreDrillPhase = "marker-a-written"
	RestoreDrillWindowReady           RestoreDrillPhase = "restore-window-ready"
	RestoreDrillMarkerBWritten        RestoreDrillPhase = "marker-b-written"
	RestoreDrillRestoreRequested      RestoreDrillPhase = "restore-requested"
	RestoreDrillTargetAvailable       RestoreDrillPhase = "target-available"
	RestoreDrillValidated             RestoreDrillPhase = "validated"
	RestoreDrillCleanupPending        RestoreDrillPhase = "cleanup-pending"
	RestoreDrillTargetDeleteRequested RestoreDrillPhase = "target-delete-requested"
	RestoreDrillTargetDeleted         RestoreDrillPhase = "target-deleted"
	RestoreDrillSourceMarkerCleaned   RestoreDrillPhase = "source-marker-cleaned"
	RestoreDrillCleaned               RestoreDrillPhase = "cleaned"
)

type RestoreDrillState struct {
	SchemaVersion          int               `json:"schema_version"`
	Phase                  RestoreDrillPhase `json:"phase"`
	DrillID                string            `json:"drill_id"`
	AccountID              string            `json:"account_id"`
	Region                 string            `json:"region"`
	SourceID               string            `json:"source_id"`
	SourceARN              string            `json:"source_arn"`
	SourceResourceID       string            `json:"source_resource_id"`
	SourceEndpoint         string            `json:"source_endpoint"`
	SourceInstanceClass    string            `json:"source_instance_class"`
	SourceAllocatedStorage int               `json:"source_allocated_storage_gib"`
	SourceSubnetGroup      string            `json:"source_subnet_group"`
	SourceSecurityGroups   []string          `json:"source_security_group_ids"`
	SourcePort             int               `json:"source_port"`
	TargetID               string            `json:"target_id"`
	TargetInstanceClass    string            `json:"target_instance_class"`
	TargetARN              string            `json:"target_arn,omitempty"`
	TargetEndpoint         string            `json:"target_endpoint,omitempty"`
	TargetResourceID       string            `json:"target_resource_id,omitempty"`
	TargetMasterSecretARN  string            `json:"target_master_secret_arn,omitempty"`
	MigrationImage         string            `json:"migration_image"`
	ApplicationSecretHash  string            `json:"application_secret_sha256"`
	MarkerPayload          string            `json:"marker_payload"`
	MarkerChecksum         string            `json:"marker_checksum,omitempty"`
	RestoreTime            time.Time         `json:"restore_time,omitempty"`
	EarliestRestorableTime time.Time         `json:"earliest_restorable_time,omitempty"`
	LatestRestorableTime   time.Time         `json:"latest_restorable_time,omitempty"`
	Tags                   map[string]string `json:"tags"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
	RestoreRequestedAt     time.Time         `json:"restore_requested_at,omitempty"`
	TargetAvailableAt      time.Time         `json:"target_available_at,omitempty"`
	ValidatedAt            time.Time         `json:"validated_at,omitempty"`
	CleanedAt              time.Time         `json:"cleaned_at,omitempty"`
	LastError              string            `json:"last_error,omitempty"`
}

func (s RestoreDrillState) Validate() error {
	var problems []error
	if s.SchemaVersion != restoreDrillStateSchemaVersion {
		problems = append(problems, fmt.Errorf("unsupported restore drill state schema %d", s.SchemaVersion))
	}
	if strings.TrimSpace(s.DrillID) == "" || strings.TrimSpace(s.SourceID) == "" || strings.TrimSpace(s.TargetID) == "" {
		problems = append(problems, errors.New("restore drill identity is incomplete"))
	}
	if s.SourceID == s.TargetID {
		problems = append(problems, errors.New("restore drill source and target must differ"))
	}
	if s.AccountID == "" || s.Region == "" {
		problems = append(problems, errors.New("restore drill account and Region are required"))
	}
	if s.SourceARN == "" || s.SourceResourceID == "" || s.SourceEndpoint == "" || s.SourceInstanceClass == "" ||
		s.SourceSubnetGroup == "" || len(s.SourceSecurityGroups) == 0 || s.SourcePort < 1 || s.SourceAllocatedStorage < 1 {
		problems = append(problems, errors.New("restore drill source identity/network contract is incomplete"))
	}
	if s.TargetInstanceClass == "" || !immutableRestoreDrillImage(s.MigrationImage) {
		problems = append(problems, errors.New("restore drill target class or immutable migration image is missing"))
	}
	if len(s.ApplicationSecretHash) != 64 || !digestPattern.MatchString("sha256:"+s.ApplicationSecretHash) {
		problems = append(problems, errors.New("restore drill application Secret checksum is invalid"))
	}
	if s.Tags["Purpose"] != "restore-drill" || s.Tags["DrillID"] != s.DrillID {
		problems = append(problems, errors.New("restore drill ownership tags are incomplete"))
	}
	if !s.Phase.valid() {
		problems = append(problems, fmt.Errorf("unsupported restore drill phase %q", s.Phase))
	}
	return errors.Join(problems...)
}

func (p RestoreDrillPhase) valid() bool {
	switch p {
	case RestoreDrillInitialized, RestoreDrillMarkerAWritten, RestoreDrillWindowReady,
		RestoreDrillMarkerBWritten, RestoreDrillRestoreRequested, RestoreDrillTargetAvailable,
		RestoreDrillValidated, RestoreDrillCleanupPending, RestoreDrillTargetDeleteRequested,
		RestoreDrillTargetDeleted, RestoreDrillSourceMarkerCleaned, RestoreDrillCleaned:
		return true
	default:
		return false
	}
}

func (p RestoreDrillPhase) needsRestoreApproval() bool {
	switch p {
	case RestoreDrillInitialized, RestoreDrillMarkerAWritten, RestoreDrillWindowReady, RestoreDrillMarkerBWritten:
		return true
	default:
		return false
	}
}

type RestoreDrillPrepared struct {
	DrillID                string
	AccountID              string
	Region                 string
	SourceID               string
	SourceARN              string
	SourceResourceID       string
	SourceEndpoint         string
	SourceInstanceClass    string
	SourceAllocatedStorage int
	SourceSubnetGroup      string
	SourceSecurityGroups   []string
	SourcePort             int
	TargetID               string
	TargetInstanceClass    string
	MigrationImage         string
	ApplicationSecretHash  string
	MarkerPayload          string
	Tags                   map[string]string
}

type RestoreDrillMarkerA struct {
	RestoreTime time.Time
	Checksum    string
}

type RestoreDrillWindow struct {
	Earliest time.Time
	Latest   time.Time
}

type RestoreDrillTarget struct {
	ARN             string
	ResourceID      string
	Endpoint        string
	MasterSecretARN string
}

type RestoreDrillStatus struct {
	SourceStatus string `json:"source_status"`
	TargetStatus string `json:"target_status,omitempty"`
	TargetExists bool   `json:"target_exists"`
}

type RestoreDrillOperations interface {
	Prepare(context.Context) (RestoreDrillPrepared, error)
	WriteMarkerA(context.Context, RestoreDrillState) (RestoreDrillMarkerA, error)
	WaitRestoreWindow(context.Context, RestoreDrillState) (RestoreDrillWindow, error)
	WriteMarkerB(context.Context, RestoreDrillState) error
	RequestRestore(context.Context, RestoreDrillState) (RestoreDrillTarget, error)
	WaitTargetAvailable(context.Context, RestoreDrillState) (RestoreDrillTarget, error)
	ValidateTarget(context.Context, RestoreDrillState) error
	ReadStatus(context.Context, RestoreDrillState) (RestoreDrillStatus, error)
	PrepareCleanup(context.Context, RestoreDrillState) error
	DeleteTarget(context.Context, RestoreDrillState) error
	WaitTargetDeleted(context.Context, RestoreDrillState) error
	CleanupSourceMarker(context.Context, RestoreDrillState) error
	VerifyClean(context.Context, RestoreDrillState) error
}

type RestoreDrillApprover interface {
	ApproveRestore(context.Context, RestoreDrillState) error
	ApproveCleanup(context.Context, RestoreDrillState) error
}

type RestoreDrillStateStore interface {
	Load() (RestoreDrillState, error)
	Save(RestoreDrillState) error
	Exists() bool
}

type FileRestoreDrillStateStore struct{ Path string }

func (s FileRestoreDrillStateStore) Exists() bool {
	_, err := os.Stat(s.Path)
	return err == nil || !os.IsNotExist(err)
}

func (s FileRestoreDrillStateStore) Load() (RestoreDrillState, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return RestoreDrillState{}, fmt.Errorf("read restore drill state: %w", err)
	}
	var state RestoreDrillState
	if err := json.Unmarshal(data, &state); err != nil {
		return RestoreDrillState{}, fmt.Errorf("decode restore drill state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return RestoreDrillState{}, err
	}
	return state, nil
}

func (s FileRestoreDrillStateStore) Save(state RestoreDrillState) error {
	if strings.TrimSpace(s.Path) == "" {
		return errors.New("restore drill state path is required")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create restore drill state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode restore drill state: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.Path), ".restore-drill-state-*.json")
	if err != nil {
		return fmt.Errorf("create restore drill state temp file: %w", err)
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
	return os.Rename(tempName, s.Path)
}

type RestoreDrillEngine struct {
	Operations RestoreDrillOperations
	Approver   RestoreDrillApprover
	State      RestoreDrillStateStore
	Evidence   *evidence.Recorder
	Clock      func() time.Time
}

func (e RestoreDrillEngine) Run(ctx context.Context) (state RestoreDrillState, err error) {
	if err := e.requireDependencies(); err != nil {
		return state, err
	}
	recorder := e.recorder("prod-restore-drill-run")
	defer func() { recorder.Finish(resultStatus(err)) }()
	if e.State.Exists() {
		state, err = e.State.Load()
		if err != nil {
			return state, err
		}
	} else {
		var prepared RestoreDrillPrepared
		if err = e.step(ctx, recorder, "preflight", func() error {
			var prepareErr error
			prepared, prepareErr = e.Operations.Prepare(ctx)
			return prepareErr
		}); err != nil {
			return state, err
		}
		now := e.now()
		state = RestoreDrillState{
			SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillInitialized,
			DrillID: prepared.DrillID, AccountID: prepared.AccountID, Region: prepared.Region,
			SourceID: prepared.SourceID, SourceARN: prepared.SourceARN, TargetID: prepared.TargetID,
			TargetInstanceClass: prepared.TargetInstanceClass,
			SourceResourceID:    prepared.SourceResourceID, SourceEndpoint: prepared.SourceEndpoint,
			SourceInstanceClass:    prepared.SourceInstanceClass,
			SourceAllocatedStorage: prepared.SourceAllocatedStorage, SourceSubnetGroup: prepared.SourceSubnetGroup,
			SourceSecurityGroups: append([]string(nil), prepared.SourceSecurityGroups...), SourcePort: prepared.SourcePort,
			MigrationImage: prepared.MigrationImage, ApplicationSecretHash: prepared.ApplicationSecretHash,
			MarkerPayload: prepared.MarkerPayload, Tags: cloneStrings(prepared.Tags),
			CreatedAt: now, UpdatedAt: now,
		}
		if err = e.State.Save(state); err != nil {
			return state, err
		}
	}
	if state.Phase == RestoreDrillCleanupPending || state.Phase == RestoreDrillCleaned {
		return state, nil
	}
	if state.Phase.needsRestoreApproval() {
		if e.Approver == nil {
			return state, errors.New("restore drill approval boundary is required")
		}
		if err = e.step(ctx, recorder, "approve-restore", func() error {
			return e.Approver.ApproveRestore(ctx, state)
		}); err != nil {
			return state, err
		}
	}
	for state.Phase != RestoreDrillCleanupPending {
		switch state.Phase {
		case RestoreDrillInitialized:
			var marker RestoreDrillMarkerA
			err = e.transition(ctx, recorder, &state, "write-marker-a", RestoreDrillMarkerAWritten, func() error {
				var operationErr error
				marker, operationErr = e.Operations.WriteMarkerA(ctx, state)
				if operationErr == nil {
					state.RestoreTime, state.MarkerChecksum = marker.RestoreTime.UTC(), marker.Checksum
				}
				return operationErr
			})
		case RestoreDrillMarkerAWritten:
			var window RestoreDrillWindow
			err = e.transition(ctx, recorder, &state, "wait-restore-window", RestoreDrillWindowReady, func() error {
				var operationErr error
				window, operationErr = e.Operations.WaitRestoreWindow(ctx, state)
				if operationErr == nil {
					state.EarliestRestorableTime, state.LatestRestorableTime = window.Earliest.UTC(), window.Latest.UTC()
				}
				return operationErr
			})
		case RestoreDrillWindowReady:
			err = e.transition(ctx, recorder, &state, "write-marker-b", RestoreDrillMarkerBWritten, func() error {
				return e.Operations.WriteMarkerB(ctx, state)
			})
		case RestoreDrillMarkerBWritten:
			var target RestoreDrillTarget
			err = e.transition(ctx, recorder, &state, "request-restore", RestoreDrillRestoreRequested, func() error {
				var operationErr error
				target, operationErr = e.Operations.RequestRestore(ctx, state)
				if operationErr == nil {
					state.TargetARN, state.TargetResourceID, state.TargetEndpoint = target.ARN, target.ResourceID, target.Endpoint
					state.TargetMasterSecretARN = target.MasterSecretARN
					state.RestoreRequestedAt = e.now()
				}
				return operationErr
			})
		case RestoreDrillRestoreRequested:
			var target RestoreDrillTarget
			err = e.transition(ctx, recorder, &state, "wait-target-available", RestoreDrillTargetAvailable, func() error {
				var operationErr error
				target, operationErr = e.Operations.WaitTargetAvailable(ctx, state)
				if operationErr == nil {
					state.TargetARN, state.TargetResourceID, state.TargetEndpoint = target.ARN, target.ResourceID, target.Endpoint
					state.TargetMasterSecretARN = target.MasterSecretARN
					state.TargetAvailableAt = e.now()
				}
				return operationErr
			})
		case RestoreDrillTargetAvailable:
			err = e.transition(ctx, recorder, &state, "validate-target", RestoreDrillValidated, func() error {
				return e.Operations.ValidateTarget(ctx, state)
			})
		case RestoreDrillValidated:
			state.Phase = RestoreDrillCleanupPending
			state.ValidatedAt = e.now()
			state.UpdatedAt = state.ValidatedAt
			err = e.State.Save(state)
		default:
			return state, fmt.Errorf("cannot run restore drill from phase %q", state.Phase)
		}
		if err != nil {
			state.LastError = sanitizeRestoreDrillError(err)
			state.UpdatedAt = e.now()
			_ = e.State.Save(state)
			return state, err
		}
	}
	return state, nil
}

func (e RestoreDrillEngine) Status(ctx context.Context) (RestoreDrillState, RestoreDrillStatus, error) {
	if err := e.requireDependencies(); err != nil {
		return RestoreDrillState{}, RestoreDrillStatus{}, err
	}
	state, err := e.State.Load()
	if err != nil {
		return state, RestoreDrillStatus{}, err
	}
	status, err := e.Operations.ReadStatus(ctx, state)
	return state, status, err
}

func (e RestoreDrillEngine) Cleanup(ctx context.Context) (state RestoreDrillState, err error) {
	if err := e.requireDependencies(); err != nil {
		return state, err
	}
	recorder := e.recorder("prod-restore-drill-cleanup")
	defer func() { recorder.Finish(resultStatus(err)) }()
	state, err = e.State.Load()
	if err != nil {
		return state, err
	}
	if state.Phase == RestoreDrillCleaned {
		return state, nil
	}
	if !state.Phase.valid() {
		return state, fmt.Errorf("cannot clean restore drill from phase %q", state.Phase)
	}
	if err = e.step(ctx, recorder, "cleanup-preflight", func() error {
		return e.Operations.PrepareCleanup(ctx, state)
	}); err != nil {
		return state, err
	}
	cleanupStarted := state.Phase == RestoreDrillCleanupPending ||
		state.Phase == RestoreDrillTargetDeleteRequested ||
		state.Phase == RestoreDrillTargetDeleted ||
		state.Phase == RestoreDrillSourceMarkerCleaned
	if !cleanupStarted || state.Phase == RestoreDrillCleanupPending {
		if e.Approver == nil {
			return state, errors.New("restore drill cleanup approval boundary is required")
		}
		if err = e.step(ctx, recorder, "approve-cleanup", func() error {
			return e.Approver.ApproveCleanup(ctx, state)
		}); err != nil {
			return state, err
		}
	}
	if !cleanupStarted {
		state.Phase = RestoreDrillCleanupPending
		state.UpdatedAt = e.now()
		if err = e.State.Save(state); err != nil {
			return state, err
		}
	}
	for state.Phase != RestoreDrillCleaned {
		switch state.Phase {
		case RestoreDrillCleanupPending:
			err = e.transition(ctx, recorder, &state, "delete-target", RestoreDrillTargetDeleteRequested, func() error {
				return e.Operations.DeleteTarget(ctx, state)
			})
		case RestoreDrillTargetDeleteRequested:
			err = e.transition(ctx, recorder, &state, "wait-target-deleted", RestoreDrillTargetDeleted, func() error {
				return e.Operations.WaitTargetDeleted(ctx, state)
			})
		case RestoreDrillTargetDeleted:
			err = e.transition(ctx, recorder, &state, "cleanup-source-marker", RestoreDrillSourceMarkerCleaned, func() error {
				return e.Operations.CleanupSourceMarker(ctx, state)
			})
		case RestoreDrillSourceMarkerCleaned:
			err = e.transition(ctx, recorder, &state, "verify-clean", RestoreDrillCleaned, func() error {
				return e.Operations.VerifyClean(ctx, state)
			})
			if err == nil {
				state.CleanedAt = e.now()
				state.UpdatedAt = state.CleanedAt
				err = e.State.Save(state)
			}
		default:
			return state, fmt.Errorf("cannot clean restore drill from phase %q", state.Phase)
		}
		if err != nil {
			state.LastError = sanitizeRestoreDrillError(err)
			state.UpdatedAt = e.now()
			_ = e.State.Save(state)
			return state, err
		}
	}
	return state, nil
}

func (e RestoreDrillEngine) transition(ctx context.Context, recorder *evidence.Recorder, state *RestoreDrillState, step string, next RestoreDrillPhase, run func() error) error {
	if err := e.step(ctx, recorder, step, run); err != nil {
		return err
	}
	state.Phase = next
	state.LastError = ""
	state.UpdatedAt = e.now()
	return e.State.Save(*state)
}

func (e RestoreDrillEngine) step(ctx context.Context, recorder *evidence.Recorder, name string, run func() error) error {
	started := e.now()
	err := run()
	status := resultStatus(err)
	recorder.Record(evidence.Event{Phase: "prod-restore-drill", Step: name, Status: status, Duration: e.now().Sub(started)})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (e RestoreDrillEngine) requireDependencies() error {
	if e.Operations == nil || e.State == nil {
		return errors.New("restore drill operations and state store are required")
	}
	return nil
}

func (e RestoreDrillEngine) recorder(operation string) *evidence.Recorder {
	if e.Evidence != nil {
		return e.Evidence
	}
	return evidence.New(operation)
}

func (e RestoreDrillEngine) now() time.Time {
	if e.Clock != nil {
		return e.Clock().UTC()
	}
	return time.Now().UTC()
}

func resultStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "passed"
}

func cloneStrings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func sanitizeRestoreDrillError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, marker := range []string{"postgres://", "postgresql://"} {
		if index := strings.Index(message, marker); index >= 0 {
			message = message[:index] + "[REDACTED_DATABASE_URL]"
		}
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
