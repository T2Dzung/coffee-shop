package prod

import (
	"testing"

	"github.com/stretchr/testify/require"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

func TestTeardownTargetsIncludeBoundedSLOCodeObject(t *testing.T) {
	t.Parallel()
	require.Contains(t, teardownTargets, "aws_s3_object.golden_journey_code")
}

func TestFoundationPlanClientDisablesSLORuntimeForTeardown(t *testing.T) {
	t.Parallel()
	original := platformterraform.Client{
		BooleanVariables: map[string]bool{
			"slo_runtime_enabled": true,
			"another_flag":        true,
		},
	}

	teardown := foundationPlanClient(original, ActionTeardown)

	require.False(t, teardown.BooleanVariables["slo_runtime_enabled"])
	require.True(t, teardown.BooleanVariables["another_flag"])
	require.True(t, original.BooleanVariables["slo_runtime_enabled"], "teardown override must not mutate the shared client")
}

func TestFoundationPlanClientPreservesSLORuntimeOutsideTeardown(t *testing.T) {
	t.Parallel()
	original := platformterraform.Client{
		BooleanVariables: map[string]bool{"slo_runtime_enabled": true},
	}

	for _, action := range []Action{ActionSetup, ActionReconcile} {
		t.Run(string(action), func(t *testing.T) {
			planned := foundationPlanClient(original, action)
			require.True(t, planned.BooleanVariables["slo_runtime_enabled"])
		})
	}
}
