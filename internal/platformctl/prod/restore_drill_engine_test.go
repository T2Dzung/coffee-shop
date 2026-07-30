package prod

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryRestoreDrillState struct {
	state  RestoreDrillState
	exists bool
	saves  []RestoreDrillPhase
}

func TestFileRestoreDrillStateStoreWritesPrivateAtomicState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private", "state.json")
	store := FileRestoreDrillStateStore{Path: path}
	state := RestoreDrillState{
		SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillInitialized,
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "source", SourceARN: "arn:source", SourceResourceID: "db-source", SourceEndpoint: "source.internal",
		SourceInstanceClass: "db.t4g.micro", SourceAllocatedStorage: 20, SourceSubnetGroup: "private",
		SourceSecurityGroups: []string{"sg-1"}, SourcePort: 5432,
		TargetID: "target", TargetInstanceClass: "db.t4g.micro", MigrationImage: testMigrationImage,
		ApplicationSecretHash: strings.Repeat("a", 64),
		Tags:                  map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}
	require.NoError(t, store.Save(state))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	loaded, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, state.DrillID, loaded.DrillID)
}

func (s *memoryRestoreDrillState) Exists() bool { return s.exists }
func (s *memoryRestoreDrillState) Load() (RestoreDrillState, error) {
	if !s.exists {
		return RestoreDrillState{}, errors.New("state is absent")
	}
	return s.state, nil
}
func (s *memoryRestoreDrillState) Save(state RestoreDrillState) error {
	s.state, s.exists = state, true
	s.saves = append(s.saves, state.Phase)
	return nil
}

type fakeRestoreDrillApprover struct{ restore, cleanup int }

func (a *fakeRestoreDrillApprover) ApproveRestore(context.Context, RestoreDrillState) error {
	a.restore++
	return nil
}
func (a *fakeRestoreDrillApprover) ApproveCleanup(context.Context, RestoreDrillState) error {
	a.cleanup++
	return nil
}

type fakeRestoreDrillOperations struct {
	calls  []string
	failAt string
}

func (f *fakeRestoreDrillOperations) call(name string) error {
	f.calls = append(f.calls, name)
	if f.failAt == name {
		return errors.New("fixture failure")
	}
	return nil
}
func (f *fakeRestoreDrillOperations) Prepare(context.Context) (RestoreDrillPrepared, error) {
	if err := f.call("prepare"); err != nil {
		return RestoreDrillPrepared{}, err
	}
	return RestoreDrillPrepared{
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "coffeeshop-prod-db", SourceARN: "arn:source", TargetID: "coffeeshop-prod-restore-o3-drill-1",
		MarkerPayload: "payload-a", Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}, nil
}
func (f *fakeRestoreDrillOperations) WriteMarkerA(context.Context, RestoreDrillState) (RestoreDrillMarkerA, error) {
	if err := f.call("write-a"); err != nil {
		return RestoreDrillMarkerA{}, err
	}
	return RestoreDrillMarkerA{RestoreTime: time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC), Checksum: "checksum"}, nil
}
func (f *fakeRestoreDrillOperations) WaitRestoreWindow(context.Context, RestoreDrillState) (RestoreDrillWindow, error) {
	if err := f.call("window"); err != nil {
		return RestoreDrillWindow{}, err
	}
	return RestoreDrillWindow{Earliest: time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC), Latest: time.Date(2026, 7, 30, 1, 1, 0, 0, time.UTC)}, nil
}
func (f *fakeRestoreDrillOperations) WriteMarkerB(context.Context, RestoreDrillState) error {
	return f.call("write-b")
}
func (f *fakeRestoreDrillOperations) RequestRestore(context.Context, RestoreDrillState) (RestoreDrillTarget, error) {
	if err := f.call("restore"); err != nil {
		return RestoreDrillTarget{}, err
	}
	return RestoreDrillTarget{ARN: "arn:target"}, nil
}
func (f *fakeRestoreDrillOperations) WaitTargetAvailable(context.Context, RestoreDrillState) (RestoreDrillTarget, error) {
	if err := f.call("wait-target"); err != nil {
		return RestoreDrillTarget{}, err
	}
	return RestoreDrillTarget{ARN: "arn:target", Endpoint: "target.internal"}, nil
}
func (f *fakeRestoreDrillOperations) ValidateTarget(context.Context, RestoreDrillState) error {
	return f.call("validate")
}
func (f *fakeRestoreDrillOperations) ReadStatus(context.Context, RestoreDrillState) (RestoreDrillStatus, error) {
	return RestoreDrillStatus{}, f.call("status")
}
func (f *fakeRestoreDrillOperations) PrepareCleanup(context.Context, RestoreDrillState) error {
	return f.call("cleanup-preflight")
}
func (f *fakeRestoreDrillOperations) DeleteTarget(context.Context, RestoreDrillState) error {
	return f.call("delete-target")
}
func (f *fakeRestoreDrillOperations) WaitTargetDeleted(context.Context, RestoreDrillState) error {
	return f.call("wait-deleted")
}
func (f *fakeRestoreDrillOperations) CleanupSourceMarker(context.Context, RestoreDrillState) error {
	return f.call("cleanup-marker")
}
func (f *fakeRestoreDrillOperations) VerifyClean(context.Context, RestoreDrillState) error {
	return f.call("verify-clean")
}

func TestRestoreDrillRunStopsAtCleanupBoundary(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{}
	approver := &fakeRestoreDrillApprover{}
	store := &memoryRestoreDrillState{}
	state, err := (RestoreDrillEngine{Operations: operations, Approver: approver, State: store}).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, RestoreDrillCleanupPending, state.Phase)
	require.Equal(t, 1, approver.restore)
	require.Equal(t, []string{"prepare", "write-a", "window", "write-b", "restore", "wait-target", "validate"}, operations.calls)
	require.NotContains(t, operations.calls, "delete-target")
}

func TestRestoreDrillRunFailsFastAndPersistsResumePhase(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{failAt: "window"}
	store := &memoryRestoreDrillState{}
	state, err := (RestoreDrillEngine{Operations: operations, Approver: &fakeRestoreDrillApprover{}, State: store}).Run(context.Background())
	require.ErrorContains(t, err, "wait-restore-window")
	require.Equal(t, RestoreDrillMarkerAWritten, state.Phase)
	require.NotEmpty(t, store.state.LastError)
	require.Equal(t, []string{"prepare", "write-a", "window"}, operations.calls)
}

func TestRestoreDrillRunResumesKnownTargetWithoutSecondApproval(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{}
	approver := &fakeRestoreDrillApprover{}
	store := &memoryRestoreDrillState{exists: true, state: RestoreDrillState{
		SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillRestoreRequested,
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "source", TargetID: "target",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}}
	state, err := (RestoreDrillEngine{Operations: operations, Approver: approver, State: store}).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, RestoreDrillCleanupPending, state.Phase)
	require.Zero(t, approver.restore)
	require.Equal(t, []string{"wait-target", "validate"}, operations.calls)
}

func TestRestoreDrillCleanupAfterValidation(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{}
	approver := &fakeRestoreDrillApprover{}
	store := &memoryRestoreDrillState{exists: true, state: RestoreDrillState{
		SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillCleanupPending,
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "coffeeshop-prod-db", TargetID: "coffeeshop-prod-restore-o3-drill-1",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}}
	state, err := (RestoreDrillEngine{Operations: operations, Approver: approver, State: store}).Cleanup(context.Background())
	require.NoError(t, err)
	require.Equal(t, RestoreDrillCleaned, state.Phase)
	require.Equal(t, 1, approver.cleanup)
	require.Equal(t, []string{"cleanup-preflight", "delete-target", "wait-deleted", "cleanup-marker", "verify-clean"}, operations.calls)

}

func TestRestoreDrillCleanupCanRecoverFromFailedRunPhase(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{}
	approver := &fakeRestoreDrillApprover{}
	store := &memoryRestoreDrillState{exists: true, state: RestoreDrillState{
		SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillMarkerBWritten,
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "coffeeshop-prod-db", TargetID: "coffeeshop-prod-restore-o3-drill-1",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}}

	state, err := (RestoreDrillEngine{Operations: operations, Approver: approver, State: store}).Cleanup(context.Background())
	require.NoError(t, err)
	require.Equal(t, RestoreDrillCleaned, state.Phase)
	require.Equal(t, 1, approver.cleanup)
	require.Contains(t, store.saves, RestoreDrillCleanupPending)
	require.Equal(t, []string{"cleanup-preflight", "delete-target", "wait-deleted", "cleanup-marker", "verify-clean"}, operations.calls)
}

func TestRestoreDrillCleanupDoesNotMutateWithoutApproval(t *testing.T) {
	t.Parallel()
	operations := &fakeRestoreDrillOperations{}
	store := &memoryRestoreDrillState{exists: true, state: RestoreDrillState{
		SchemaVersion: restoreDrillStateSchemaVersion, Phase: RestoreDrillRestoreRequested,
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "coffeeshop-prod-db", TargetID: "coffeeshop-prod-restore-o3-drill-1",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}}

	_, err := (RestoreDrillEngine{Operations: operations, State: store}).Cleanup(context.Background())
	require.ErrorContains(t, err, "approval boundary")
	require.Equal(t, RestoreDrillRestoreRequested, store.state.Phase)
	require.Equal(t, []string{"cleanup-preflight"}, operations.calls)
}
