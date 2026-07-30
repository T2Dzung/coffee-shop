package prod

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeardownTargetsIncludeBoundedSLOCodeObject(t *testing.T) {
	t.Parallel()
	require.Contains(t, teardownTargets, "aws_s3_object.golden_journey_code")
}
