package ci

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
	Parts []PlanPart
}

type PlanPart struct {
	Name     string
	Artifact platformterraform.Plan
	Human    string
}

func (p Plan) Empty() bool {
	for _, part := range p.Parts {
		if part.Artifact.Summary != (platformterraform.Summary{}) {
			return false
		}
	}
	return true
}

func (p Plan) Cleanup() {
	for _, part := range p.Parts {
		_ = part.Artifact.Cleanup()
	}
}

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
		return fmt.Errorf("CI operations are required")
	}
	if !action.Valid() {
		return fmt.Errorf("unsupported CI action %q", action)
	}
	recorder := e.Evidence
	if recorder == nil {
		recorder = evidence.New("ci-" + string(action))
	}
	defer func() {
		status := "passed"
		if err != nil {
			status = "failed"
		}
		recorder.Finish(status)
	}()
	if err = e.step(ctx, recorder, "preflight", func() error {
		return e.Operations.Preflight(ctx, action)
	}); err != nil {
		return err
	}
	if action == ActionStatus {
		return e.step(ctx, recorder, "verify", func() error { return e.Operations.Verify(ctx, action) })
	}
	var plan Plan
	if err = e.step(ctx, recorder, "plan", func() error {
		var planErr error
		plan, planErr = e.Operations.Plan(ctx, action)
		return planErr
	}); err != nil {
		return err
	}
	defer plan.Cleanup()
	if plan.Empty() {
		if action == ActionSetup {
			if err = e.step(ctx, recorder, "configure", func() error { return e.Operations.Configure(ctx) }); err != nil {
				return err
			}
		}
		return e.step(ctx, recorder, "verify", func() error { return e.Operations.Verify(ctx, action) })
	}
	if e.Approver == nil {
		return fmt.Errorf("approval boundary is required for %s", action)
	}
	if err = e.step(ctx, recorder, "approve", func() error {
		return e.Approver.Approve(ctx, action, plan)
	}); err != nil {
		return err
	}
	if err = e.step(ctx, recorder, "apply", func() error { return e.Operations.Apply(ctx, plan) }); err != nil {
		return err
	}
	if action == ActionSetup {
		if err = e.step(ctx, recorder, "configure", func() error { return e.Operations.Configure(ctx) }); err != nil {
			return err
		}
	}
	return e.step(ctx, recorder, "verify", func() error { return e.Operations.Verify(ctx, action) })
}

func (e Engine) step(_ context.Context, recorder *evidence.Recorder, name string, run func() error) error {
	started := time.Now()
	err := run()
	status := "passed"
	if err != nil {
		status = "failed"
	}
	recorder.Record(evidence.Event{Phase: "ci", Step: name, Status: status, Duration: time.Since(started)})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
