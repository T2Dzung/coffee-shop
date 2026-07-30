package prod

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestoreDrillApprovalCannotUseTerraformAutoApprove(t *testing.T) {
	t.Parallel()
	approver := ConsoleApprover{Input: strings.NewReader(""), Output: &bytes.Buffer{}, AutoApprove: true}
	state := RestoreDrillState{AccountID: "123456789012", Region: "ap-southeast-1", SourceID: "source", TargetID: "target"}
	require.Error(t, approver.ApproveRestore(context.Background(), state))
	require.Error(t, approver.ApproveCleanup(context.Background(), state))
}

func TestRestoreDrillApprovalUsesDistinctLiterals(t *testing.T) {
	t.Parallel()
	state := RestoreDrillState{AccountID: "123456789012", Region: "ap-southeast-1", SourceID: "source", TargetID: "target"}
	require.NoError(t, (ConsoleApprover{Input: strings.NewReader("restore\n"), Output: &bytes.Buffer{}}).
		ApproveRestore(context.Background(), state))
	require.NoError(t, (ConsoleApprover{Input: strings.NewReader("cleanup\n"), Output: &bytes.Buffer{}}).
		ApproveCleanup(context.Background(), state))
}
