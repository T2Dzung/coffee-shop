package iximiuz

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type captureRunner struct{ request command.Request }

func (r *captureRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.request = req
	return command.Result{}, nil
}

func TestLabSSHInputAndTimeout(t *testing.T) {
	r := &captureRunner{}
	c := Client{Runner: r, Binary: "labctl", RemoteTimeout: 30 * time.Minute}
	_, err := c.RemoteInput(context.Background(), strings.NewReader("source bytes"), "tar", "-xf", "-")
	require.NoError(t, err)
	data, err := io.ReadAll(r.request.Stdin)
	require.NoError(t, err)
	require.Equal(t, "source bytes", string(data))
	require.Equal(t, 30*time.Minute, r.request.Timeout)
	require.False(t, r.request.Stream)
}

func TestLabSSHArgumentQuoting(t *testing.T) {
	r := &captureRunner{}
	c := Client{Runner: r, Binary: "labctl", RunID: strings.Repeat("a", 24), Machine: "dev-machine"}
	input := "a'b $(printf INJECTED); * with spaces"
	_, err := c.Remote(context.Background(), "printf", "%s", input)
	require.NoError(t, err)
	require.False(t, r.request.Stream)
	require.NotContains(t, r.request.Args, "--forward-agent")
	// Execute only benign printf locally to prove the SSH command string preserves
	// the literal argument instead of evaluating shell metacharacters.
	out, err := (command.OSRunner{}).Run(context.Background(), command.Request{Name: "bash", Args: []string{"-c", r.request.Args[len(r.request.Args)-1]}})
	require.NoError(t, err)
	require.Equal(t, input, out.Stdout)
	_, err = c.Remote(context.Background(), "bad\x00arg")
	require.Error(t, err)
	_, err = c.Remote(context.Background())
	require.Error(t, err)
}
