package dev

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type fakeOperations struct {
	calls  []string
	failAt string
	plan   Plan
}

func (f *fakeOperations) call(name string) error {
	f.calls = append(f.calls, name)
	if name == f.failAt {
		return errors.New("injected failure")
	}
	return nil
}

func (f *fakeOperations) Preflight(context.Context, Action) error { return f.call("preflight") }
func (f *fakeOperations) Plan(context.Context, Action) (Plan, error) {
	return f.plan, f.call("plan")
}
func (f *fakeOperations) Apply(context.Context, Plan) error { return f.call("apply") }
func (f *fakeOperations) Configure(context.Context) error   { return f.call("configure") }
func (f *fakeOperations) Verify(context.Context, Action) error {
	return f.call("verify")
}

type fakeApprover struct{ calls *[]string }

func (f fakeApprover) Approve(context.Context, Action, Plan) error {
	*f.calls = append(*f.calls, "approve")
	return nil
}

func changedPlan() Plan {
	return Plan{Artifact: platformterraform.Plan{Summary: platformterraform.Summary{Create: 1}}}
}

func TestDEVSetupSequence(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{plan: changedPlan()}
	require.NoError(t, (Engine{Operations: operations, Approver: fakeApprover{&operations.calls}}).
		Run(context.Background(), ActionSetup))
	require.Equal(t, []string{"preflight", "plan", "approve", "apply", "configure", "verify"}, operations.calls)
}

func TestDEVSetupReconfiguresWhenPlanIsEmpty(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	require.NoError(t, (Engine{Operations: operations}).Run(context.Background(), ActionSetup))
	require.Equal(t, []string{"preflight", "plan", "configure", "verify"}, operations.calls)
}

func TestDEVTeardownDoesNotConfigure(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{plan: changedPlan()}
	require.NoError(t, (Engine{Operations: operations, Approver: fakeApprover{&operations.calls}}).
		Run(context.Background(), ActionTeardown))
	require.Equal(t, []string{"preflight", "plan", "approve", "apply", "verify"}, operations.calls)
}

func TestDEVStatusNeverPlans(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	require.NoError(t, (Engine{Operations: operations}).Run(context.Background(), ActionStatus))
	require.Equal(t, []string{"preflight", "verify"}, operations.calls)
}

func TestDEVFailureStopsMutation(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{failAt: "plan", plan: changedPlan()}
	require.ErrorContains(t, (Engine{Operations: operations}).Run(context.Background(), ActionSetup), "plan")
	require.Equal(t, []string{"preflight", "plan"}, operations.calls)
}
