// Package iximiuz is the transport adapter for labctl, not a lifecycle owner.
package iximiuz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type Status struct {
	ID         string `json:"id"`
	Playground string `json:"playground"`
	State      string `json:"state"`
	PageURL    string `json:"pageUrl"`
}

type Client struct {
	Runner        command.Runner
	Binary        string
	RunID         string
	Machine       string
	RemoteTimeout time.Duration
}

func (c Client) Native(ctx context.Context, args ...string) (string, error) {
	result, err := c.Runner.Run(ctx, command.Request{
		Name: c.Binary, Args: args, Timeout: 10 * time.Minute,
	})
	if err != nil {
		return "", fmt.Errorf("labctl operation: %w", err)
	}
	return result.Stdout, nil
}

func (c Client) Status(ctx context.Context) (Status, error) {
	output, err := c.Native(ctx, "playground", "status", c.RunID, "--output", "json")
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return status, fmt.Errorf("decode labctl status: %w", err)
	}
	return status, nil
}

// SSH transports a command string. Quote each argv element separately; no shell
// program, expansion, pipeline or user-provided snippet is ever constructed here.
func (c Client) Remote(ctx context.Context, argv ...string) (string, error) {
	return c.RemoteInput(ctx, nil, argv...)
}

// RemoteInput sends only explicit input bytes; it never forwards local files or credentials implicitly.
func (c Client) RemoteInput(ctx context.Context, input io.Reader, argv ...string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("remote command is required")
	}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return "", fmt.Errorf("NUL in remote argument")
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	timeout := c.RemoteTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	result, err := c.Runner.Run(ctx, command.Request{
		Name:    c.Binary,
		Args:    []string{"ssh", c.RunID, "--machine", c.Machine, "--", strings.Join(quoted, " ")},
		Timeout: timeout,
		Stdin:   input,
		// Never stream: env/kubeconfig reads are preflight inputs, not logs.
	})
	if err != nil {
		return "", fmt.Errorf("remote %s: %w", argv[0], err)
	}
	return result.Stdout, nil
}
