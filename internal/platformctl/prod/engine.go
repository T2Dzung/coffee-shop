package prod

import (
	"context"
	"fmt"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type Action string

const (
	ActionSetup      Action = "setup"
	ActionReconcile  Action = "reconcile"
	ActionStatus     Action = "status"
	ActionResilience Action = "resilience"
	ActionTeardown   Action = "teardown"
)

type Plan struct {
	Artifact platformterraform.Plan
	Human    string
}

func (p Plan) Empty() bool {
	return p.Artifact.Summary == (platformterraform.Summary{})
}

type Operations interface {
	Preflight(context.Context, Action) error
	Bootstrap(context.Context) error
	Plan(context.Context, Action) (Plan, error)
	BeforeApply(context.Context, Action) error
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
		return fmt.Errorf("PROD operations are required")
	}
	if !action.Valid() {
		return fmt.Errorf("unsupported PROD action %q", action)
	}
	recorder := e.Evidence
	if recorder == nil {
		recorder = evidence.New("prod-" + string(action))
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

	switch action {
	case ActionSetup:
		if err = e.step(ctx, recorder, "bootstrap", func() error {
			return e.Operations.Bootstrap(ctx)
		}); err != nil {
			return err
		}
		return e.mutateAndVerify(ctx, recorder, action, true)
	case ActionReconcile:
		return e.mutateAndVerify(ctx, recorder, action, false)
	case ActionStatus, ActionResilience:
		return e.step(ctx, recorder, "verify", func() error {
			return e.Operations.Verify(ctx, action)
		})
	case ActionTeardown:
		if err = e.mutate(ctx, recorder, action); err != nil {
			return err
		}
		return e.step(ctx, recorder, "verify", func() error {
			return e.Operations.Verify(ctx, action)
		})
	}
	return nil
}

func (a Action) Valid() bool {
	switch a {
	case ActionSetup, ActionReconcile, ActionStatus, ActionResilience, ActionTeardown:
		return true
	default:
		return false
	}
}

func (e Engine) mutateAndVerify(ctx context.Context, recorder *evidence.Recorder, action Action, configure bool) error {
	if err := e.mutate(ctx, recorder, action); err != nil {
		return err
	}
	if configure {
		if err := e.step(ctx, recorder, "configure", func() error {
			return e.Operations.Configure(ctx)
		}); err != nil {
			return err
		}
	}
	return e.step(ctx, recorder, "verify", func() error {
		return e.Operations.Verify(ctx, action)
	})
}

func (e Engine) mutate(ctx context.Context, recorder *evidence.Recorder, action Action) error {
	var plan Plan
	if err := e.step(ctx, recorder, "plan", func() error {
		var err error
		plan, err = e.Operations.Plan(ctx, action)
		return err
	}); err != nil {
		return err
	}
	defer plan.Artifact.Cleanup()
	recorder.Record(evidence.Event{
		Phase: "prod", Step: "plan-artifact", Status: "recorded",
		Details: map[string]any{
			"sha256":  plan.Artifact.Fingerprint,
			"create":  plan.Artifact.Summary.Create,
			"update":  plan.Artifact.Summary.Update,
			"delete":  plan.Artifact.Summary.Delete,
			"replace": plan.Artifact.Summary.Replace,
		},
	})
	if plan.Empty() {
		recorder.Record(evidence.Event{
			Phase: "prod", Step: "apply", Status: "skipped-empty-plan",
		})
		return nil
	}
	if e.Approver == nil {
		return fmt.Errorf("approval boundary is required for %s", action)
	}
	if err := e.step(ctx, recorder, "approve", func() error {
		return e.Approver.Approve(ctx, action, plan)
	}); err != nil {
		return err
	}
	if err := e.step(ctx, recorder, "before-apply", func() error {
		return e.Operations.BeforeApply(ctx, action)
	}); err != nil {
		return err
	}
	return e.step(ctx, recorder, "apply", func() error {
		return e.Operations.Apply(ctx, plan)
	})
}

func (e Engine) step(ctx context.Context, recorder *evidence.Recorder, name string, run func() error) error {
	started := time.Now()
	err := run()
	status := "passed"
	if err != nil {
		status = "failed"
	}
	recorder.Record(evidence.Event{
		Phase: "prod", Step: name, Status: status, Duration: time.Since(started),
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
