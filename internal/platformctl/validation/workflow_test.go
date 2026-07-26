package validation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeWorkflowFindsSecurityBoundaries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
on:
  pull_request:
jobs:
  test:
    runs-on: [self-hosted, linux]
    secrets: inherit
    steps:
      - uses: actions/checkout@main
      - uses: actions/setup-go@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      - uses: ./.github/actions/local
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.UsesSecretsInherit)
	require.True(t, input.PullRequestSelfHosted)
	require.Equal(t, []string{"actions/checkout@main"}, input.UnpinnedActions)
}

func TestNormalizeWorkflowAllowsHostedPinnedWorkflow(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
on:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.False(t, input.UsesSecretsInherit)
	require.False(t, input.PullRequestSelfHosted)
	require.Empty(t, input.UnpinnedActions)
}
