package kubernetes

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type Client struct {
	Runner     command.Runner
	Kubeconfig string
	Timeout    time.Duration
}

func (c Client) Kubectl(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	result, err := c.Runner.Run(ctx, command.Request{
		Name: "kubectl", Args: args, Stdin: stdin,
		Env:     map[string]string{"KUBECONFIG": c.Kubeconfig},
		Timeout: c.Timeout,
	})
	return strings.TrimSpace(result.Stdout), err
}

func (c Client) CanI(ctx context.Context, verb, resource, namespace, identity string) (bool, error) {
	result, err := c.Runner.Run(ctx, command.Request{
		Name: "kubectl",
		Args: []string{
			"auth", "can-i", verb, resource,
			"-n", namespace, "--as", identity,
		},
		Env:     map[string]string{"KUBECONFIG": c.Kubeconfig},
		Timeout: c.Timeout,
	})
	answer := strings.TrimSpace(result.Stdout)
	switch answer {
	case "yes":
		if err != nil {
			return false, fmt.Errorf("kubectl auth can-i returned yes with an error: %w", err)
		}
		return true, nil
	case "no":
		// kubectl uses exit code 1 for a valid authorization denial.
		if err == nil || result.ExitCode == 1 {
			return false, nil
		}
		return false, fmt.Errorf("kubectl auth can-i denial failed unexpectedly: %w", err)
	default:
		if err != nil {
			return false, fmt.Errorf("kubectl auth can-i: %w", err)
		}
		return false, fmt.Errorf("kubectl auth can-i returned unexpected answer %q", answer)
	}
}

func (c Client) Helm(ctx context.Context, args ...string) (string, error) {
	result, err := c.Runner.Run(ctx, command.Request{
		Name: "helm", Args: args,
		Env:     map[string]string{"KUBECONFIG": c.Kubeconfig},
		Timeout: c.Timeout, Stream: true,
	})
	return strings.TrimSpace(result.Stdout), err
}

func (c Client) WaitFor(ctx context.Context, attempts int, interval time.Duration, description string, observe func(context.Context) (bool, error)) error {
	for attempt := 1; attempt <= attempts; attempt++ {
		ready, err := observe(ctx)
		if err == nil && ready {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}
	return fmt.Errorf("%s did not become ready after %d attempts", description, attempts)
}
