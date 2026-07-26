package prod

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type ConsoleApprover struct {
	Input       io.Reader
	Output      io.Writer
	AutoApprove bool
}

func (a ConsoleApprover) Approve(ctx context.Context, action Action, plan Plan) error {
	if a.Output == nil {
		a.Output = io.Discard
	}
	fmt.Fprintf(a.Output,
		"Terraform plan summary: create=%d update=%d delete=%d replace=%d\nSaved plan SHA-256: %s\n",
		plan.Artifact.Summary.Create,
		plan.Artifact.Summary.Update,
		plan.Artifact.Summary.Delete,
		plan.Artifact.Summary.Replace,
		plan.Artifact.Fingerprint,
	)
	if plan.Human != "" {
		fmt.Fprintln(a.Output, plan.Human)
	}
	if a.AutoApprove {
		fmt.Fprintln(a.Output, "Explicit auto-approval enabled for this operation.")
		return nil
	}
	if a.Input == nil {
		return fmt.Errorf("interactive approval input is unavailable")
	}
	expected := "apply"
	if action == ActionTeardown {
		expected = "teardown"
	}
	fmt.Fprintf(a.Output, "Type %q once to authorize %s: ", expected, action)
	answer := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		value, err := bufio.NewReader(a.Input).ReadString('\n')
		answer <- struct {
			value string
			err   error
		}{strings.TrimSpace(value), err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case response := <-answer:
		if response.err != nil && response.value == "" {
			return fmt.Errorf("read approval: %w", response.err)
		}
		if response.value != expected {
			return fmt.Errorf("%s cancelled: expected %q", action, expected)
		}
		return nil
	}
}
