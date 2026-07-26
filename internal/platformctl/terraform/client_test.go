package terraform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestDecodeSummary(t *testing.T) {
	t.Parallel()
	summary, err := DecodeSummary([]byte(`{"resource_changes":[
		{"change":{"actions":["create"]}},
		{"change":{"actions":["update"]}},
		{"change":{"actions":["delete"]}},
		{"change":{"actions":["delete","create"]}}
	]}`))
	require.NoError(t, err)
	require.Equal(t, Summary{Create: 1, Update: 1, Delete: 1, Replace: 1}, summary)
}

func TestApplyRejectsChangedSavedPlan(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "plan")
	require.NoError(t, os.WriteFile(path, []byte("approved"), 0o600))
	fingerprint, err := Fingerprint(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0o600))
	fake := &command.FakeRunner{}
	client := Client{Runner: fake, Dir: "/tf", DataDir: "/data"}

	err = client.Apply(context.Background(), Plan{BinaryPath: path, Fingerprint: fingerprint})
	require.ErrorContains(t, err, "fingerprint changed")
	require.Empty(t, fake.Requests)
}

func TestCreatePlanPassesBooleanVariableWithoutStringQuoting(t *testing.T) {
	t.Parallel()
	// CreatePlan owns a randomized saved-plan path, so use a recording runner that
	// stops after the plan command and inspect the stable variable argument.
	recorder := &recordingErrorRunner{}
	client := Client{
		Runner: recorder, Dir: "/tf", DataDir: "/data",
		BooleanVariables: map[string]bool{"dev_runtime_enabled": true},
	}
	_, _ = client.CreatePlan(context.Background(), t.TempDir(), "bool", false, nil)
	require.NotEmpty(t, recorder.requests)
	require.Contains(t, recorder.requests[0].Args, "-var=dev_runtime_enabled=true")
}

type recordingErrorRunner struct{ requests []command.Request }

func (r *recordingErrorRunner) Run(_ context.Context, request command.Request) (command.Result, error) {
	r.requests = append(r.requests, request)
	return command.Result{}, os.ErrInvalid
}
