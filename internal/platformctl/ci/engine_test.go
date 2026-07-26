package ci

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

func changingPlan() Plan {
	return Plan{Parts: []PlanPart{{
		Name: "foundation", Artifact: platformterraform.Plan{Summary: platformterraform.Summary{Create: 1}},
	}}}
}

func TestCISetupSequence(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{plan: changingPlan()}
	require.NoError(t, (Engine{Operations: operations, Approver: fakeApprover{&operations.calls}}).
		Run(context.Background(), ActionSetup))
	require.Equal(t, []string{"preflight", "plan", "approve", "apply", "configure", "verify"}, operations.calls)
}

func TestCISetupReconfiguresARCWhenTerraformPlanIsEmpty(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	require.NoError(t, (Engine{Operations: operations}).Run(context.Background(), ActionSetup))
	require.Equal(t, []string{"preflight", "plan", "configure", "verify"}, operations.calls)
}

func TestCITeardownSequence(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{plan: changingPlan()}
	require.NoError(t, (Engine{Operations: operations, Approver: fakeApprover{&operations.calls}}).
		Run(context.Background(), ActionTeardown))
	require.Equal(t, []string{"preflight", "plan", "approve", "apply", "verify"}, operations.calls)
}

func TestCIStatusDoesNotPlanOrMutate(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	require.NoError(t, (Engine{Operations: operations}).Run(context.Background(), ActionStatus))
	require.Equal(t, []string{"preflight", "verify"}, operations.calls)
}

func TestCIStopsAfterPreflightFailure(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{failAt: "preflight"}
	require.ErrorContains(t, (Engine{Operations: operations}).Run(context.Background(), ActionSetup), "preflight")
	require.Equal(t, []string{"preflight"}, operations.calls)
}

func TestCIRejectsUnknownActionBeforePreflight(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	require.ErrorContains(t, (Engine{Operations: operations}).Run(context.Background(), Action("unknown")), "unsupported")
	require.Empty(t, operations.calls)
}
