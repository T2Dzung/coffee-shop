package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateCacheContractKeepsMatrixWritersIsolated(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", ".."))

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	require.NoError(t, err)
	action, err := os.ReadFile(filepath.Join(root, ".github", "actions", "build-component", "action.yml"))
	require.NoError(t, err)
	runnerValues, err := os.ReadFile(filepath.Join(root, "infrastructure", "k8s", "ci", "arc", "runner-values.yaml"))
	require.NoError(t, err)

	// These strings are the shell/YAML runtime contract. The explicit assertions
	// prevent the shared writable cache regression recorded in the 2026-07-18 incident.
	require.Contains(t, string(workflow), `module_cache="${cache_root}/mod/${COMPONENT}"`)
	require.Contains(t, string(workflow), `build_cache="${cache_root}/build/${COMPONENT}"`)
	require.Contains(t, string(workflow), "group: release-candidate-${{ github.event.repository.default_branch }}")
	require.Contains(t, string(workflow), "cancel-in-progress: false")
	require.NotContains(t, string(workflow), "GOMODCACHE: /go-cache/pkg/mod")

	require.Contains(t, string(action), `if [[ "${USE_HOSTED_CACHE}" == "true" ]]`)
	require.Contains(t, string(action), `type=gha,scope=candidate-${COMPONENT}`)

	require.Contains(t, string(runnerValues), "persistentVolumeClaim:\n          claimName: go-cache")
	require.Contains(t, string(runnerValues), "fsGroup: 1000")
	require.Contains(t, string(runnerValues), "name: docker-storage\n        emptyDir:")
	require.NotContains(t, string(runnerValues), "name: GOMODCACHE")
}

func TestEmergencySourceReconciliationSkipsDuplicateCandidateBuild(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", ".."))

	candidateWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	require.NoError(t, err)
	deliveryWorkflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "_reusable-prod-delivery.yml"))
	require.NoError(t, err)

	// The marker is a textual cross-workflow contract: only the machine-generated
	// emergency reconciliation PR emits it, and both candidate jobs that can report
	// status must honor it. Normal source merges remain eligible for candidate builds.
	require.Equal(t, 2, strings.Count(string(candidateWorkflow), "[skip candidate]"))
	require.Contains(t, string(deliveryWorkflow), "reconcile emergency source [skip candidate]")
}
