package validation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestEmergencyLineageAcceptsSourceAlreadyOnDefaultBranch(t *testing.T) {
	repository, baseline, source := emergencyLineageRepository(t, true)

	result := runEmergencyLineage(t, repository, baseline, source)

	require.Equal(t, "true", strings.TrimSpace(result.Stdout))
}

func TestEmergencyLineageRequiresReconciliationForUnmergedSource(t *testing.T) {
	repository, baseline, source := emergencyLineageRepository(t, false)

	result := runEmergencyLineage(t, repository, baseline, source)

	require.Equal(t, "false", strings.TrimSpace(result.Stdout))
}

func emergencyLineageRepository(t *testing.T, sourceOnMain bool) (string, string, string) {
	t.Helper()
	repository := t.TempDir()
	runner := command.OSRunner{}
	run := func(args ...string) string {
		t.Helper()
		result, err := runner.Run(context.Background(), command.Request{
			Name: "git", Args: args, Dir: repository, Timeout: 10 * time.Second,
		})
		require.NoError(t, err, result.Stderr)
		return strings.TrimSpace(result.Stdout)
	}

	run("init", "--initial-branch=main")
	run("config", "user.name", "Emergency Test")
	run("config", "user.email", "emergency-test@example.invalid")
	componentDir := filepath.Join(repository, "platform-ownership-guard")
	require.NoError(t, os.MkdirAll(componentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(componentDir, "guard.go"), []byte("package guard\n"), 0o600))
	run("add", ".")
	run("commit", "-m", "baseline")
	baseline := run("rev-parse", "HEAD")

	if !sourceOnMain {
		run("switch", "-c", "hotfix")
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(componentDir, "guard.go"),
		[]byte("package guard\n\nconst enabled = true\n"),
		0o600,
	))
	run("add", ".")
	run("commit", "-m", "hotfix")
	source := run("rev-parse", "HEAD")

	if !sourceOnMain {
		run("switch", "main")
		require.NoError(t, os.WriteFile(filepath.Join(repository, "README.md"), []byte("# main\n"), 0o600))
		run("add", ".")
		run("commit", "-m", "advance main")
	}
	return repository, baseline, source
}

func runEmergencyLineage(t *testing.T, repository, baseline, source string) command.Result {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	script, err := filepath.Abs(filepath.Join(root, "scripts", "ci", "validate-emergency-lineage.sh"))
	require.NoError(t, err)
	changedFiles := filepath.Join(t.TempDir(), "changed-files.txt")
	patch := filepath.Join(t.TempDir(), "reconcile.patch")
	result, err := (command.OSRunner{}).Run(context.Background(), command.Request{
		Name: "bash",
		Args: []string{
			script, source, baseline, "refs/heads/main", changedFiles, patch,
		},
		Dir: repository, Timeout: 10 * time.Second,
	})
	require.NoError(t, err, result.Stderr)

	changed, err := os.ReadFile(changedFiles)
	require.NoError(t, err)
	require.Equal(t, "platform-ownership-guard/guard.go\n", string(changed))
	info, err := os.Stat(patch)
	require.NoError(t, err)
	require.Positive(t, info.Size())
	return result
}
