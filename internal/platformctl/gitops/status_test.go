package gitops

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluate(t *testing.T) {
	t.Parallel()
	require.NoError(t, Evaluate([]byte(`{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`)))
	require.ErrorContains(t, Evaluate([]byte(`{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Healthy"}}}`)), "OutOfSync")
	require.ErrorContains(t, Evaluate([]byte(`{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"},"conditions":[{"type":"ComparisonError","message":"render"}]}}`)), "ComparisonError")
}
