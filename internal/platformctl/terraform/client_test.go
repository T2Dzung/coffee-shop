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
