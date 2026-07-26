package policy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestTerraformPolicyUsesExplicitNamespace(t *testing.T) {
	t.Parallel()
	root := "/repo"
	input := "/tmp/plan.json"
	fake := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "conftest",
		Args: []string{
			"test", "--policy", filepath.Join(root, "policy", "terraform"),
			"--namespace", "terraform.reconcile", "--output", "json", input,
		},
	}}}
	err := (Evaluator{Runner: fake, ProjectRoot: root}).Terraform(context.Background(), "reconcile", input)
	require.NoError(t, err)
	require.NoError(t, fake.Verify())
}

func TestTerraformPolicyFailsClosedOnUnknownName(t *testing.T) {
	t.Parallel()
	err := (Evaluator{}).Terraform(context.Background(), "unknown", "/tmp/plan.json")
	require.ErrorContains(t, err, "unsupported")
}
