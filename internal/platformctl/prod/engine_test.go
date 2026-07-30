package prod

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type fakeOperations struct {
	calls         []string
	failAt        string
	planSummary   platformterraform.Summary
	planSummaries []platformterraform.Summary
}

func (f *fakeOperations) call(name string) error {
	f.calls = append(f.calls, name)
	if name == f.failAt {
		return errors.New("injected failure")
	}
	return nil
}
func (f *fakeOperations) Preflight(context.Context, Action) error { return f.call("preflight") }
func (f *fakeOperations) Bootstrap(context.Context) error         { return f.call("bootstrap") }
func (f *fakeOperations) Plan(context.Context, Action) (Plan, error) {
	summary := f.planSummary
	if len(f.planSummaries) > 0 {
		summary = f.planSummaries[0]
		f.planSummaries = f.planSummaries[1:]
	}
	return Plan{Artifact: platformterraform.Plan{Summary: summary}}, f.call("plan")
}
func (f *fakeOperations) BeforeApply(context.Context, Action) error {
	return f.call("before-apply")
}
func (f *fakeOperations) Apply(context.Context, Plan) error    { return f.call("apply") }
func (f *fakeOperations) Configure(context.Context) error      { return f.call("configure") }
func (f *fakeOperations) Verify(context.Context, Action) error { return f.call("verify") }

type fakeApprover struct{ calls int }

func (f *fakeApprover) Approve(context.Context, Action, Plan) error { f.calls++; return nil }

func TestSetupStateMachine(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{planSummary: platformterraform.Summary{Create: 1}}
	approver := &fakeApprover{}
	err := (Engine{Operations: operations, Approver: approver}).Run(context.Background(), ActionSetup)
	require.NoError(t, err)
	require.Equal(t, []string{"preflight", "bootstrap", "plan", "before-apply", "apply", "configure", "plan", "before-apply", "apply", "verify"}, operations.calls)
	require.Equal(t, 2, approver.calls)
}

func TestSetupStopsAtFailure(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{failAt: "apply", planSummary: platformterraform.Summary{Create: 1}}
	err := (Engine{Operations: operations, Approver: &fakeApprover{}}).Run(context.Background(), ActionSetup)
	require.ErrorContains(t, err, "apply")
	require.Equal(t, []string{"preflight", "bootstrap", "plan", "before-apply", "apply"}, operations.calls)
}

func TestSetupSkipsApprovalAndApplyForEmptyConvergencePlan(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{planSummaries: []platformterraform.Summary{{Create: 1}, {}}}
	approver := &fakeApprover{}

	err := (Engine{Operations: operations, Approver: approver}).Run(context.Background(), ActionSetup)

	require.NoError(t, err)
	require.Equal(t, []string{"preflight", "bootstrap", "plan", "before-apply", "apply", "configure", "plan", "verify"}, operations.calls)
	require.Equal(t, 1, approver.calls)
}

func TestTeardownHasNoConfigureOrRuntimeVerify(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{planSummary: platformterraform.Summary{Delete: 1}}
	err := (Engine{Operations: operations, Approver: &fakeApprover{}}).Run(context.Background(), ActionTeardown)
	require.NoError(t, err)
	require.Equal(t, []string{"preflight", "plan", "before-apply", "apply", "verify"}, operations.calls)
}

func TestReconcileEmptyPlanNeedsNoApprovalOrApply(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	approver := &fakeApprover{}
	err := (Engine{Operations: operations, Approver: approver}).Run(context.Background(), ActionReconcile)
	require.NoError(t, err)
	require.Equal(t, []string{"preflight", "plan", "verify"}, operations.calls)
	require.Zero(t, approver.calls)
}

func TestInvalidActionDoesNotRunPreflight(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{}
	err := (Engine{Operations: operations, Approver: &fakeApprover{}}).
		Run(context.Background(), Action("invalid"))
	require.ErrorContains(t, err, "unsupported")
	require.Empty(t, operations.calls)
}
