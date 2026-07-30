package validation

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProdMaintenancePromotionAcknowledgesMigrationBoundary(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	workflow := decodeYAMLMap(t, filepath.Join(root, ".github", "workflows", "prod-promote.yml"))
	resolveJob := asMap(asMap(workflow["jobs"])["resolve"])

	var resolveScript string
	steps, _ := resolveJob["steps"].([]any)
	for _, raw := range steps {
		step := asMap(raw)
		if step["name"] == "Resolve component matrix" {
			resolveScript, _ = step["run"].(string)
			break
		}
	}
	require.NotEmpty(t, resolveScript)

	// This is a textual runtime contract because the lane-to-CLI acknowledgement
	// is executed by the workflow shell. Standard promotion must keep the generic
	// migration safeguard closed; only maintenance may open it explicitly.
	require.Contains(t, resolveScript, "allow_migration=false")
	require.Contains(t, resolveScript, `if [[ "${LANE}" == "maintenance" ]]; then`)
	require.Contains(t, resolveScript, "allow_migration=true")
	require.Contains(t, resolveScript, `--allow-migration="${allow_migration}"`)

	submitJob := asMap(asMap(workflow["jobs"])["submit-reviewed"])
	var autoMerge any
	steps, _ = submitJob["steps"].([]any)
	for _, raw := range steps {
		step := asMap(raw)
		if step["uses"] == "./.github/actions/submit-gitops-pr" {
			autoMerge = asMap(step["with"])["auto-merge"]
			break
		}
	}
	require.Equal(t, "true", autoMerge,
		"manual workflow dispatch is the maintenance decision; the protected PR should auto-merge after required checks")
}
