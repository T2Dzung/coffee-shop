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

func (a ConsoleApprover) ApproveRestore(ctx context.Context, state RestoreDrillState) error {
	if a.Output == nil {
		a.Output = io.Discard
	}
	restoreTime := "selected after marker A commits; exact T is persisted before target creation"
	if !state.RestoreTime.IsZero() {
		restoreTime = state.RestoreTime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	fmt.Fprintf(a.Output, `Restore drill Gate A:
  account/Region : %s / %s
  source         : %s
  target         : %s
  restore time   : %s
  target policy  : private, single-AZ, backup retention 0, deletion protection off
`, state.AccountID, state.Region, state.SourceID, state.TargetID, restoreTime)
	return a.approveLiteral(ctx, "restore", "restore drill")
}

func (a ConsoleApprover) ApproveCleanup(ctx context.Context, state RestoreDrillState) error {
	if a.Output == nil {
		a.Output = io.Discard
	}
	fmt.Fprintf(a.Output, `Restore drill Gate B:
  account/Region : %s / %s
  delete target  : %s
  preserve source: %s
  cleanup marker : %s
`, state.AccountID, state.Region, state.TargetID, state.SourceID, state.DrillID)
	return a.approveLiteral(ctx, "cleanup", "restore drill cleanup")
}

func (a ConsoleApprover) approveLiteral(ctx context.Context, expected, operation string) error {
	if a.Input == nil {
		return fmt.Errorf("interactive approval input is unavailable")
	}
	fmt.Fprintf(a.Output, "Type %q once to authorize %s: ", expected, operation)
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
			return fmt.Errorf("%s cancelled: expected %q", operation, expected)
		}
		return nil
	}
}
