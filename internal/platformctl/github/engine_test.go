package github

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type fakeOperations struct {
	plan       Plan
	existing   map[string]struct{}
	set        []string
	applied    bool
	verified   bool
	applyError error
}

func (f *fakeOperations) Plan(context.Context) (Plan, error) { return f.plan, nil }
func (f *fakeOperations) Apply(context.Context, Plan) error {
	f.applied = true
	return f.applyError
}
func (f *fakeOperations) ExistingSecrets(context.Context) (map[string]struct{}, error) {
	return f.existing, nil
}
func (f *fakeOperations) SetSecret(_ context.Context, name, _ string) error {
	f.set = append(f.set, name)
	return nil
}
func (f *fakeOperations) Verify(context.Context) error {
	f.verified = true
	return nil
}

type fakeApprover struct{ called bool }

func (a *fakeApprover) Approve(context.Context, Plan, []string) error {
	a.called = true
	return nil
}

func TestBootstrapAppliesThenUpdatesConfiguredSecrets(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{
		plan: Plan{Artifact: platformterraform.Plan{Summary: platformterraform.Summary{Create: 3}}},
		existing: map[string]struct{}{
			"GITOPS_PR_TOKEN": {}, "TELEGRAM_TO": {}, "TELEGRAM_TOKEN": {},
		},
	}
	approver := &fakeApprover{}
	err := (Engine{
		Operations: operations, Approver: approver,
		Secrets: map[string]string{"TELEGRAM_TOKEN": "secret"},
	}).Bootstrap(context.Background())
	require.NoError(t, err)
	require.True(t, approver.called)
	require.True(t, operations.applied)
	require.Equal(t, []string{"TELEGRAM_TOKEN"}, operations.set)
	require.True(t, operations.verified)
}

func TestBootstrapRejectsMissingUnconfiguredSecretBeforeApproval(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{
		plan:     Plan{Artifact: platformterraform.Plan{}},
		existing: map[string]struct{}{"GITOPS_PR_TOKEN": {}, "TELEGRAM_TOKEN": {}},
	}
	approver := &fakeApprover{}
	err := (Engine{Operations: operations, Approver: approver}).Bootstrap(context.Background())
	require.ErrorContains(t, err, "TELEGRAM_TO")
	require.False(t, approver.called)
	require.False(t, operations.applied)
}

func TestBootstrapRejectsDestructivePlan(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{
		plan: Plan{Artifact: platformterraform.Plan{Summary: platformterraform.Summary{Delete: 1}}},
	}
	approver := &fakeApprover{}
	err := (Engine{Operations: operations, Approver: approver}).Bootstrap(context.Background())
	require.ErrorContains(t, err, "delete or replacement")
	require.False(t, approver.called)
	require.False(t, operations.applied)
}

func TestDoctorRejectsTerraformDriftBeforeCheckingNames(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{
		plan: Plan{Artifact: platformterraform.Plan{Summary: platformterraform.Summary{Update: 1}}},
	}
	err := (Engine{Operations: operations}).Doctor(context.Background())
	require.ErrorContains(t, err, "Terraform drift")
	require.False(t, operations.verified)
}

func TestDoctorChecksNamesAfterEmptyPlan(t *testing.T) {
	t.Parallel()
	operations := &fakeOperations{plan: Plan{Artifact: platformterraform.Plan{}}}
	err := (Engine{Operations: operations}).Doctor(context.Background())
	require.NoError(t, err)
	require.True(t, operations.verified)
}
