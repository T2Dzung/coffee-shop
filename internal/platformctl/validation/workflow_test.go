package validation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestNormalizeWorkflowFindsSecurityBoundaries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
on:
  pull_request:
jobs:
  test:
    runs-on: [self-hosted, linux]
    secrets: inherit
    steps:
      - uses: actions/checkout@main
      - uses: actions/setup-go@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      - uses: ./.github/actions/local
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.UsesSecretsInherit)
	require.True(t, input.PullRequestSelfHosted)
	require.Equal(t, []string{"actions/checkout@main"}, input.UnpinnedActions)
	require.Equal(t, []string{"actions/setup-go@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, input.PinnedActions)
}

func TestVerifyPinnedActionsDeduplicatesRepositories(t *testing.T) {
	t.Parallel()
	checkoutSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	setupSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	runner := &command.FakeRunner{Expectations: []command.Expectation{
		{
			Name: "git", Args: []string{"ls-remote", "https://github.com/actions/checkout.git"},
			Result: command.Result{Stdout: checkoutSHA + "\trefs/tags/v7\n"},
		},
		{
			Name: "git", Args: []string{"ls-remote", "https://github.com/actions/setup-go.git"},
			Result: command.Result{Stdout: setupSHA + "\trefs/tags/v6^{}\n"},
		},
	}}
	validator := Validator{Runner: runner}
	require.NoError(t, validator.verifyPinnedActions(context.Background(), []string{
		"actions/setup-go@" + setupSHA,
		"actions/checkout@" + checkoutSHA,
		"actions/checkout@" + checkoutSHA,
	}))
	require.NoError(t, runner.Verify())
}

func TestVerifyPinnedActionsRejectsUnresolvableCommit(t *testing.T) {
	t.Parallel()
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "git", Args: []string{"ls-remote", "https://github.com/anchore/sbom-action.git"},
		Result: command.Result{Stdout: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/tags/v0.20.4\n"},
	}}}
	err := (Validator{Runner: runner}).verifyPinnedActions(context.Background(), []string{
		"anchore/sbom-action@" + sha,
	})
	require.ErrorContains(t, err, "not advertised by upstream Git refs")
}

func TestNormalizeWorkflowAllowsHostedPinnedWorkflow(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
on:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.False(t, input.UsesSecretsInherit)
	require.False(t, input.PullRequestSelfHosted)
	require.Empty(t, input.UnpinnedActions)
}

func TestNormalizeWorkflowFindsCandidateExecutionContractViolations(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: candidate
jobs:
  preflight-candidate-ecr:
    runs-on: self-hosted
  build-candidate:
    needs: [detect-components]
    runs-on: trusted-build
    steps:
      - run: docker info
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.CandidateBuildWithoutECRPreflight)
	require.True(t, input.CandidatePreflightNotHosted)
	require.True(t, input.CandidateBuildMissingToolchain)
}

func TestNormalizeWorkflowFindsAWSCLIInARCBuildAction(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "action.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: Build immutable component
runs:
  using: composite
  steps:
    - shell: bash
      run: aws ecr describe-images
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.ARCBuildUsesAWSCLI)
}

func TestNormalizeWorkflowRequiresAtomicProdReleaseSetFanIn(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: PROD — Promote QA-Approved Digest
jobs:
  standard:
    strategy:
      matrix:
        component: [web, proxy]
    uses: ./.github/workflows/_reusable-prod-delivery.yml
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.ProdStandardMissingAtomicFanIn)
}

func TestNormalizeWorkflowAllowsAtomicProdReleaseSetFanIn(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: PROD — Promote QA-Approved Digest
jobs:
  copy-standard:
    strategy:
      matrix:
        component: [web, proxy]
  submit-standard:
    needs: [copy-standard]
    steps:
      - uses: ./.github/actions/submit-gitops-pr
  promotion-status:
    if: >-
      always() && !cancelled() &&
      (github.event_name == 'workflow_dispatch' ||
       github.ref == format('refs/heads/{0}', github.event.repository.default_branch))
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.False(t, input.ProdStandardMissingAtomicFanIn)
	require.False(t, input.ProdStatusMissingDefaultBranchGate)
}

func TestNormalizeWorkflowRejectsUngatedProdStatusJob(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workflow.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
name: PROD — Promote QA-Approved Digest
jobs:
  copy-standard:
    strategy:
      matrix:
        component: [web]
  submit-standard:
    needs: [copy-standard]
    steps:
      - uses: ./.github/actions/submit-gitops-pr
  promotion-status:
    if: always() && !cancelled()
`), 0o600))
	input, err := normalizeWorkflow(path)
	require.NoError(t, err)
	require.True(t, input.ProdStatusMissingDefaultBranchGate)
}
