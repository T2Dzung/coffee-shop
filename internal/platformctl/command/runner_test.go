package command

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOSRunnerRedactsOutput(t *testing.T) {
	t.Parallel()
	runner := OSRunner{}
	result, err := runner.Run(context.Background(), Request{
		Name: "sh", Args: []string{"-c", "printf token-secret; printf token-secret >&2; exit 7"},
		Redactions: []string{"token-secret"},
	})
	require.Error(t, err)
	require.Equal(t, 7, result.ExitCode)
	require.NotContains(t, result.Stdout+result.Stderr+err.Error(), "token-secret")
	require.Contains(t, result.Stdout, "[REDACTED]")
}

func TestOSRunnerTimeout(t *testing.T) {
	t.Parallel()
	_, err := (OSRunner{}).Run(context.Background(), Request{
		Name: "sh", Args: []string{"-c", "sleep 2"}, Timeout: 20 * time.Millisecond,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "signal"))
}

func TestOSRunnerNeverLiveStreamsUnredactedSecret(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	result, err := (OSRunner{Stdout: &output}).Run(context.Background(), Request{
		Name:       "sh",
		Args:       []string{"-c", "printf token-secret"},
		Stream:     true,
		Redactions: []string{"token-secret"},
	})
	require.NoError(t, err)
	require.Equal(t, "[REDACTED]", output.String())
	require.Equal(t, "[REDACTED]", result.Stdout)
}
