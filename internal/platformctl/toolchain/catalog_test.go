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
`), 0o600))
	catalog, err := Load(path)
	require.NoError(t, err)
	tool, err := catalog.Find("actionlint")
	require.NoError(t, err)
	require.Equal(t, "v1.7.12", tool.Version)
}
