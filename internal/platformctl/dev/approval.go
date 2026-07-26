package dev

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
		"DEV Terraform plan: create=%d update=%d delete=%d replace=%d\nSaved plan SHA-256: %s\n",
		plan.Artifact.Summary.Create, plan.Artifact.Summary.Update, plan.Artifact.Summary.Delete,
		plan.Artifact.Summary.Replace, plan.Artifact.Fingerprint)
	if plan.Human != "" {
		fmt.Fprintln(a.Output, plan.Human)
	}
	if a.AutoApprove {
		fmt.Fprintln(a.Output, "Explicit DEV auto-approval enabled.")
		return nil
	}
	expected := "apply"
	if action == ActionTeardown {
		expected = "teardown"
	}
	fmt.Fprintf(a.Output, "Type %q once to authorize DEV %s: ", expected, action)
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
			return fmt.Errorf("DEV %s cancelled: expected %q", action, expected)
		}
		return nil
	}
}
