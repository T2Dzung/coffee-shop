package kubernetes

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestCanIAllowsAuthorizedIdentity(t *testing.T) {
	t.Parallel()
	client, runner := canITestClient(command.Result{Stdout: "yes\n"}, nil)

	allowed, err := client.CanI(context.Background(), "get", "deployments.apps", "coffeeshop", "guard")

	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, runner.Verify())
}

func TestCanITreatsExitOneAsValidDenial(t *testing.T) {
	t.Parallel()
	client, runner := canITestClient(
		command.Result{Stdout: "no\n", ExitCode: 1},
		errors.New("kubectl exited with code 1"),
	)

	allowed, err := client.CanI(context.Background(), "patch", "deployments.apps", "coffeeshop", "guard")

	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, runner.Verify())
}

func TestCanIRejectsCommandFailure(t *testing.T) {
	t.Parallel()
	client, runner := canITestClient(
		command.Result{Stderr: "connection refused", ExitCode: 2},
		errors.New("kubectl exited with code 2"),
	)

	allowed, err := client.CanI(context.Background(), "patch", "deployments.apps", "coffeeshop", "guard")

	require.ErrorContains(t, err, "kubectl auth can-i")
	require.False(t, allowed)
	require.NoError(t, runner.Verify())
}

func canITestClient(result command.Result, err error) (Client, *command.FakeRunner) {
	args := []string{
		"auth", "can-i", "patch", "deployments.apps",
		"-n", "coffeeshop", "--as", "guard",
	}
	if result.Stdout == "yes\n" {
		args[2] = "get"
	}
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "kubectl", Args: args, Result: result, Err: err,
	}}}
	return Client{Runner: runner, Kubeconfig: "/tmp/test-kubeconfig"}, runner
}
