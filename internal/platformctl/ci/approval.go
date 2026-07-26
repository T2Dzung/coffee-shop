package ci

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
	for _, part := range plan.Parts {
		fmt.Fprintf(a.Output, "CI Terraform %s plan: create=%d update=%d delete=%d replace=%d\nSaved plan SHA-256: %s\n",
			part.Name, part.Artifact.Summary.Create, part.Artifact.Summary.Update, part.Artifact.Summary.Delete,
			part.Artifact.Summary.Replace, part.Artifact.Fingerprint)
		if part.Human != "" {
			fmt.Fprintln(a.Output, part.Human)
		}
	}
	if a.AutoApprove {
		fmt.Fprintln(a.Output, "Explicit CI auto-approval enabled.")
		return nil
	}
	expected := "apply"
	if action == ActionTeardown {
		expected = "teardown"
	}
	fmt.Fprintf(a.Output, "Type %q once to authorize CI %s: ", expected, action)
	answer := make(chan string, 1)
	go func() {
		value, _ := bufio.NewReader(a.Input).ReadString('\n')
		answer <- strings.TrimSpace(value)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case value := <-answer:
		if value != expected {
			return fmt.Errorf("CI %s cancelled: expected %q", action, expected)
		}
		return nil
	}
}
