package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRestoreDrillStore struct {
	aTime      time.Time
	checksum   string
	validation restoreDrillOutput
	calls      []string
	err        error
}

func (f *fakeRestoreDrillStore) WriteMarkerA(context.Context, string, string) (time.Time, string, error) {
	f.calls = append(f.calls, "write-a")
	return f.aTime, f.checksum, f.err
}
func (f *fakeRestoreDrillStore) WriteMarkerB(context.Context, string, string) error {
	f.calls = append(f.calls, "write-b")
	return f.err
}
func (f *fakeRestoreDrillStore) Validate(context.Context, string, string) (restoreDrillOutput, error) {
	f.calls = append(f.calls, "validate")
	return f.validation, f.err
}
func (f *fakeRestoreDrillStore) Cleanup(context.Context, string) error {
	f.calls = append(f.calls, "cleanup")
	return f.err
}

func TestRunRestoreDrillWriteAReportsPostCommitRestoreTime(t *testing.T) {
	t.Parallel()
	wantTime := time.Date(2026, 7, 30, 6, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	store := &fakeRestoreDrillStore{aTime: wantTime, checksum: "abc"}
	got, err := runRestoreDrill(context.Background(), store, restoreDrillInput{
		Action: "write-a", DrillID: "o3-20260730", Payload: "payload-a",
	})
	if err != nil {
		t.Fatalf("runRestoreDrill() error = %v", err)
	}
	if !got.RestoreTime.Equal(wantTime.UTC()) || got.Checksum != "abc" {
		t.Fatalf("runRestoreDrill() = %#v", got)
	}
}

func TestRunRestoreDrillRejectsInvalidInputBeforeMutation(t *testing.T) {
	t.Parallel()
	store := &fakeRestoreDrillStore{}
	_, err := runRestoreDrill(context.Background(), store, restoreDrillInput{
		Action: "write-a", DrillID: "../bad", Payload: "payload",
	})
	if err == nil {
		t.Fatal("runRestoreDrill() error = nil, want rejection")
	}
	if len(store.calls) != 0 {
		t.Fatalf("store calls = %v, want none", store.calls)
	}
}

func TestRunRestoreDrillProbeRequiresNoMarkerMutation(t *testing.T) {
	t.Parallel()
	store := &fakeRestoreDrillStore{}
	output, err := runRestoreDrill(context.Background(), store, restoreDrillInput{
		Action: "probe", DrillID: "o3-drill-123",
	})
	if err != nil || output.Action != "probe" || len(store.calls) != 0 {
		t.Fatalf("probe output=%#v calls=%v err=%v", output, store.calls, err)
	}
}

func TestRunRestoreDrillPropagatesValidationFailure(t *testing.T) {
	t.Parallel()
	store := &fakeRestoreDrillStore{err: errors.New("marker B present")}
	_, err := runRestoreDrill(context.Background(), store, restoreDrillInput{
		Action: "validate", DrillID: "o3-20260730", ExpectedChecksum: "abc",
	})
	if err == nil {
		t.Fatal("runRestoreDrill() error = nil, want validation failure")
	}
}

func TestMarkerChecksumBindsDrillMarkerAndPayload(t *testing.T) {
	t.Parallel()
	base := markerChecksum("o3-20260730", "A", "payload")
	for _, changed := range []string{
		markerChecksum("o3-20260731", "A", "payload"),
		markerChecksum("o3-20260730", "B", "payload"),
		markerChecksum("o3-20260730", "A", "other"),
	} {
		if base == changed {
			t.Fatal("markerChecksum() did not bind every input")
		}
	}
}
