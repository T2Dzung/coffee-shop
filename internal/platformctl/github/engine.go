package github

import (
	"context"
	"fmt"
	"sort"

	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

var RequiredRepositorySecrets = []string{"GITOPS_PR_TOKEN", "TELEGRAM_TO", "TELEGRAM_TOKEN"}

type Plan struct {
	Artifact platformterraform.Plan
	Human    string
}

type Operations interface {
	Plan(context.Context) (Plan, error)
	Apply(context.Context, Plan) error
	ExistingSecrets(context.Context) (map[string]struct{}, error)
	SetSecret(context.Context, string, string) error
	Verify(context.Context) error
}

type Approver interface {
	Approve(context.Context, Plan, []string) error
}

type Engine struct {
	Operations Operations
	Approver   Approver
	Secrets    map[string]string
}

func (e Engine) Bootstrap(ctx context.Context) error {
	if e.Operations == nil || e.Approver == nil {
		return fmt.Errorf("GitHub operations and approval boundary are required")
	}
	plan, err := e.Operations.Plan(ctx)
	if err != nil {
		return err
	}
	defer plan.Artifact.Cleanup()
	if plan.Artifact.Summary.Delete > 0 || plan.Artifact.Summary.Replace > 0 {
		return fmt.Errorf("GitHub governance plan contains delete or replacement actions")
	}
	existing, err := e.Operations.ExistingSecrets(ctx)
	if err != nil {
		return err
	}
	updates := make([]string, 0, len(e.Secrets))
	for name := range e.Secrets {
		updates = append(updates, name)
	}
	sort.Strings(updates)
	for _, name := range RequiredRepositorySecrets {
		if _, configured := e.Secrets[name]; configured {
			continue
		}
		if _, present := existing[name]; !present {
			return fmt.Errorf("required repository secret %s is absent and has no local secret file", name)
		}
	}
	if err := e.Approver.Approve(ctx, plan, updates); err != nil {
		return err
	}
	if err := e.Operations.Apply(ctx, plan); err != nil {
		return err
	}
	for _, name := range updates {
		if err := e.Operations.SetSecret(ctx, name, e.Secrets[name]); err != nil {
			return fmt.Errorf("set repository secret %s: %w", name, err)
		}
	}
	return e.Operations.Verify(ctx)
}

func (e Engine) Doctor(ctx context.Context) error {
	if e.Operations == nil {
		return fmt.Errorf("GitHub operations are required")
	}
	plan, err := e.Operations.Plan(ctx)
	if err != nil {
		return err
	}
	defer plan.Artifact.Cleanup()
	if plan.Artifact.Summary != (platformterraform.Summary{}) {
		return fmt.Errorf("GitHub governance has Terraform drift: %+v", plan.Artifact.Summary)
	}
	return e.Operations.Verify(ctx)
}
