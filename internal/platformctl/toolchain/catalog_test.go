package toolchain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAndFind(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "toolchain.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
schemaVersion: 1
tools:
  actionlint:
    version: v1.7.12
profiles:
  candidate-runner:
    requiredCommands: [bash, docker]
`), 0o600))
	catalog, err := Load(path)
	require.NoError(t, err)
	tool, err := catalog.Find("actionlint")
	require.NoError(t, err)
	require.Equal(t, "v1.7.12", tool.Version)
	require.NoError(t, catalog.VerifyProfile("candidate-runner", func(command string) (string, error) {
		return "/usr/bin/" + command, nil
	}))
}

func TestVerifyProfileReportsEveryMissingCommand(t *testing.T) {
	t.Parallel()
	catalog := Catalog{Profiles: map[string]Profile{
		"candidate-runner": {RequiredCommands: []string{"git", "docker", "jq"}},
	}}
	err := catalog.VerifyProfile("candidate-runner", func(command string) (string, error) {
		if command == "git" {
			return "/usr/bin/git", nil
		}
		return "", os.ErrNotExist
	})
	require.ErrorContains(t, err, "[docker jq]")
}
