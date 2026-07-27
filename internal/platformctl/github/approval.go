package github

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

func (a ConsoleApprover) Approve(ctx context.Context, plan Plan, secretNames []string) error {
	if a.Output == nil {
		a.Output = io.Discard
	}
	fmt.Fprintf(a.Output,
		"GitHub plan summary: create=%d update=%d delete=%d replace=%d\nSaved plan SHA-256: %s\n",
		plan.Artifact.Summary.Create, plan.Artifact.Summary.Update,
		plan.Artifact.Summary.Delete, plan.Artifact.Summary.Replace,
		plan.Artifact.Fingerprint,
	)
	if plan.Human != "" {
		fmt.Fprintln(a.Output, plan.Human)
	}
	if len(secretNames) > 0 {
		fmt.Fprintf(a.Output, "Repository secrets updated after apply: %s\n", strings.Join(secretNames, ", "))
	} else {
		fmt.Fprintln(a.Output, "Existing repository secrets will be preserved.")
	}
	if a.AutoApprove {
		fmt.Fprintln(a.Output, "Explicit GitHub auto-approval enabled.")
		return nil
	}
	fmt.Fprint(a.Output, `Type "apply" once to authorize this GitHub governance transaction: `)
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
		if response.value != "apply" {
			return fmt.Errorf(`GitHub bootstrap cancelled: expected "apply"`)
		}
		return nil
	}
}
