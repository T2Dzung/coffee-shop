package dev

import (
	"context"
	"fmt"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type Action string

const (
	ActionSetup    Action = "setup"
	ActionStatus   Action = "status"
	ActionTeardown Action = "teardown"
)

func (a Action) Valid() bool {
	return a == ActionSetup || a == ActionStatus || a == ActionTeardown
}

type Plan struct {
	Artifact platformterraform.Plan
	Human    string
}

func (p Plan) Empty() bool { return p.Artifact.Summary == (platformterraform.Summary{}) }

type Operations interface {
	Preflight(context.Context, Action) error
	Plan(context.Context, Action) (Plan, error)
	Apply(context.Context, Plan) error
	Configure(context.Context) error
	Verify(context.Context, Action) error
}

type Approver interface {
	Approve(context.Context, Action, Plan) error
}

type Engine struct {
	Operations Operations
	Approver   Approver
	Evidence   *evidence.Recorder
}

func (e Engine) Run(ctx context.Context, action Action) (err error) {
	if e.Operations == nil {
		return fmt.Errorf("DEV operations are required")
	}
	if !action.Valid() {
		return fmt.Errorf("unsupported DEV action %q", action)
	}
	recorder := e.Evidence
	if recorder == nil {
		recorder = evidence.New("dev-" + string(action))
	}
	defer func() {
		status := "passed"
		if err != nil {
			status = "failed"
		}
		recorder.Finish(status)
	}()
	if err = e.step(recorder, "preflight", func() error { return e.Operations.Preflight(ctx, action) }); err != nil {
		return err
	}
	if action == ActionStatus {
		return e.step(recorder, "verify", func() error { return e.Operations.Verify(ctx, action) })
	}
	var plan Plan
	if err = e.step(recorder, "plan", func() error {
		var planErr error
		plan, planErr = e.Operations.Plan(ctx, action)
		return planErr
	}); err != nil {
		return err
	}
	defer plan.Artifact.Cleanup()
	recorder.Record(evidence.Event{Phase: "dev", Step: "plan-artifact", Status: "recorded", Details: map[string]any{
		"sha256": plan.Artifact.Fingerprint, "create": plan.Artifact.Summary.Create,
		"update": plan.Artifact.Summary.Update, "delete": plan.Artifact.Summary.Delete,
		"replace": plan.Artifact.Summary.Replace,
	}})
	if !plan.Empty() {
		if e.Approver == nil {
			return fmt.Errorf("approval boundary is required for DEV %s", action)
		}
		if err = e.step(recorder, "approve", func() error { return e.Approver.Approve(ctx, action, plan) }); err != nil {
			return err
		}
		if err = e.step(recorder, "apply", func() error { return e.Operations.Apply(ctx, plan) }); err != nil {
			return err
		}
	} else {
		recorder.Record(evidence.Event{Phase: "dev", Step: "apply", Status: "skipped-empty-plan"})
	}
	if action == ActionSetup {
		if err = e.step(recorder, "configure", func() error { return e.Operations.Configure(ctx) }); err != nil {
			return err
		}
	}
	return e.step(recorder, "verify", func() error { return e.Operations.Verify(ctx, action) })
}

func (e Engine) step(recorder *evidence.Recorder, name string, run func() error) error {
	started := time.Now()
	err := run()
	status := "passed"
	if err != nil {
		status = "failed"
	}
	recorder.Record(evidence.Event{Phase: "dev", Step: name, Status: status, Duration: time.Since(started)})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
