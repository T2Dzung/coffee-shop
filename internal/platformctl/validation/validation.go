package validation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/policy"
)

type Profile string
type Scope string

const (
	ProfileAll     Profile = "all"
	ProfileDev     Profile = "dev"
	ProfileProd    Profile = "prod"
	ProfileCI      Profile = "ci"
	ScopeAll       Scope   = "all"
	ScopeTerraform Scope   = "terraform"
	ScopePlatform  Scope   = "platform"
)

type Validator struct {
	Runner      command.Runner
	ProjectRoot string
	Profile     Profile
	Scope       Scope
}

func (v Validator) Run(ctx context.Context) error {
	if v.Runner == nil {
		return fmt.Errorf("validation runner is required")
	}
	switch v.Profile {
	case ProfileAll, ProfileDev, ProfileProd, ProfileCI:
	default:
		return fmt.Errorf("unsupported validation profile %q", v.Profile)
	}
	if v.Scope == "" {
		v.Scope = ScopeAll
	}
	switch v.Scope {
	case ScopeAll, ScopeTerraform, ScopePlatform:
	default:
		return fmt.Errorf("unsupported validation scope %q", v.Scope)
	}
	if _, err := component.Load(filepath.Join(v.ProjectRoot, "platform", "components.yaml")); err != nil {
		return err
	}
	if err := v.run(ctx, "", nil, "go", "test", "./internal/platformctl/..."); err != nil {
		return fmt.Errorf("platformctl tests: %w", err)
	}
	if err := (policy.Evaluator{Runner: v.Runner, ProjectRoot: v.ProjectRoot}).Verify(ctx); err != nil {
		return fmt.Errorf("policy tests: %w", err)
	}
	if v.Scope != ScopePlatform {
		if err := v.terraform(ctx); err != nil {
			return err
		}
	}
	if v.Scope != ScopeTerraform {
		if err := v.shell(ctx); err != nil {
			return err
		}
	}
	if v.Scope != ScopeTerraform && (v.Profile == ProfileAll || v.Profile == ProfileCI) {
		if err := v.workflows(ctx); err != nil {
			return err
		}
	}
	if v.Scope != ScopeTerraform && (v.Profile == ProfileAll || v.Profile == ProfileProd) {
		if err := v.kubernetes(ctx, "prod"); err != nil {
			return err
		}
	}
	if v.Scope != ScopeTerraform && (v.Profile == ProfileAll || v.Profile == ProfileCI) {
		if err := v.kubernetes(ctx, "ci"); err != nil {
			return err
		}
		if err := v.ansible(ctx, true); err != nil {
			return err
		}
	}
	if v.Scope != ScopeTerraform && (v.Profile == ProfileAll || v.Profile == ProfileDev) {
		if err := v.kubernetes(ctx, "dev"); err != nil {
			return err
		}
		if err := v.ansible(ctx, false); err != nil {
			return err
		}
	}
	return nil
}

func (v Validator) terraform(ctx context.Context) error {
	if err := v.run(ctx, "", nil, "terraform", "-chdir="+filepath.Join(v.ProjectRoot, "infrastructure", "terraform"), "fmt", "-check", "-recursive"); err != nil {
		return fmt.Errorf("terraform fmt: %w", err)
	}
	roots := []string{}
	if v.Profile == ProfileAll || v.Profile == ProfileDev {
		roots = append(roots, "bootstrap/dev", "envs/dev")
	}
	if v.Profile == ProfileAll || v.Profile == ProfileProd {
		roots = append(roots, "bootstrap/prod", "envs/prod")
	}
	if v.Profile == ProfileAll || v.Profile == ProfileCI {
		roots = append(roots, "envs/ci")
	}
	for _, root := range roots {
		dir := filepath.Join(v.ProjectRoot, "infrastructure", "terraform", root)
		dataDir, err := os.MkdirTemp("", "platformctl-tf-validate-")
		if err != nil {
			return err
		}
		env := map[string]string{"TF_DATA_DIR": dataDir}
		if err := v.run(ctx, "", env, "terraform", "-chdir="+dir, "init", "-backend=false", "-input=false", "-no-color"); err != nil {
			os.RemoveAll(dataDir)
			return fmt.Errorf("terraform init %s: %w", root, err)
		}
		if err := v.run(ctx, "", env, "terraform", "-chdir="+dir, "validate", "-no-color"); err != nil {
			os.RemoveAll(dataDir)
			return fmt.Errorf("terraform validate %s: %w", root, err)
		}
		os.RemoveAll(dataDir)
	}
	return nil
}

func (v Validator) shell(ctx context.Context) error {
	patterns := []string{
		filepath.Join(v.ProjectRoot, "scripts", "*.sh"),
		filepath.Join(v.ProjectRoot, "scripts", "ci", "*.sh"),
		filepath.Join(v.ProjectRoot, "scripts", "rehearsal", "*.sh"),
		filepath.Join(v.ProjectRoot, "scripts", "validation", "*.sh"),
	}
	files := []string{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("no shell files discovered")
	}
	args := append([]string{}, files...)
	if err := v.run(ctx, "", nil, "shellcheck", args...); err != nil {
		return fmt.Errorf("shellcheck: %w", err)
	}
	return nil
}

func (v Validator) workflows(ctx context.Context) error {
	files, err := filepath.Glob(filepath.Join(v.ProjectRoot, ".github", "workflows", "*.yml"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("no GitHub workflows discovered")
	}
	if err := v.run(ctx, "", nil, "actionlint", files...); err != nil {
		return fmt.Errorf("actionlint: %w", err)
	}
	evaluator := policy.Evaluator{Runner: v.Runner, ProjectRoot: v.ProjectRoot}
	pinnedActions := make([]string, 0)
	for _, file := range files {
		input, err := normalizeWorkflow(file)
		if err != nil {
			return err
		}
		if err := evaluator.Workflow(ctx, input); err != nil {
			return fmt.Errorf("workflow policy %s: %w", filepath.Base(file), err)
		}
		pinnedActions = append(pinnedActions, input.PinnedActions...)
	}
	actions, err := filepath.Glob(filepath.Join(v.ProjectRoot, ".github", "actions", "*", "action.yml"))
	if err != nil {
		return err
	}
	sort.Strings(actions)
	for _, file := range actions {
		input, err := normalizeWorkflow(file)
		if err != nil {
			return err
		}
		if err := evaluator.Workflow(ctx, input); err != nil {
			return fmt.Errorf("composite action policy %s: %w", filepath.Base(filepath.Dir(file)), err)
		}
		pinnedActions = append(pinnedActions, input.PinnedActions...)
	}
	return v.verifyPinnedActions(ctx, pinnedActions)
}

func (v Validator) verifyPinnedActions(ctx context.Context, references []string) error {
	type pins struct {
		shas map[string]struct{}
	}
	byRepository := map[string]*pins{}
	for _, reference := range references {
		repository, sha, err := parsePinnedAction(reference)
		if err != nil {
			return err
		}
		entry := byRepository[repository]
		if entry == nil {
			entry = &pins{shas: map[string]struct{}{}}
			byRepository[repository] = entry
		}
		entry.shas[sha] = struct{}{}
	}
	repositories := make([]string, 0, len(byRepository))
	for repository := range byRepository {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		result, err := v.Runner.Run(ctx, command.Request{
			Name: "git", Args: []string{"ls-remote", "https://github.com/" + repository + ".git"},
			Timeout: 2 * time.Minute,
		})
		if err != nil {
			return fmt.Errorf("resolve pinned actions from %s: %w", repository, err)
		}
		for sha := range byRepository[repository].shas {
			if !strings.Contains(result.Stdout, sha+"\t") {
				return fmt.Errorf("pinned action commit %s@%s is not advertised by upstream Git refs", repository, sha)
			}
		}
	}
	return nil
}

func parsePinnedAction(reference string) (string, string, error) {
	separator := strings.LastIndex(reference, "@")
	if separator < 1 || separator == len(reference)-1 {
		return "", "", fmt.Errorf("invalid pinned action reference %q", reference)
	}
	pathParts := strings.Split(reference[:separator], "/")
	if len(pathParts) < 2 || pathParts[0] == "" || pathParts[1] == "" {
		return "", "", fmt.Errorf("invalid pinned action repository %q", reference)
	}
	sha := reference[separator+1:]
	if len(sha) != 40 {
		return "", "", fmt.Errorf("invalid pinned action commit %q", reference)
	}
	return pathParts[0] + "/" + pathParts[1], sha, nil
}

func (v Validator) kubernetes(ctx context.Context, environment string) error {
	var roots []string
	switch environment {
	case "prod":
		roots = []string{
			filepath.Join(v.ProjectRoot, "infrastructure/k8s/apps/coffeeshop/overlays/prod"),
			filepath.Join(v.ProjectRoot, "infrastructure/k8s/environments/prod/platform"),
			filepath.Join(v.ProjectRoot, "platform-ownership-guard/config/prod"),
		}
	case "dev":
		roots = []string{
			filepath.Join(v.ProjectRoot, "infrastructure/k8s/apps/coffeeshop/overlays/dev"),
		}
	case "ci":
		roots = []string{filepath.Join(v.ProjectRoot, "infrastructure/k8s/ci/arc")}
	default:
		return fmt.Errorf("unsupported Kubernetes environment %q", environment)
	}
	for _, root := range roots {
		result, err := v.Runner.Run(ctx, command.Request{
			Name: "kubectl", Args: []string{"kustomize", root},
			Timeout: 2 * time.Minute,
		})
		if err != nil {
			return fmt.Errorf("render %s: %w", root, err)
		}
		rendered, err := os.CreateTemp("", "platformctl-kubernetes-*.yaml")
		if err != nil {
			return err
		}
		renderedPath := rendered.Name()
		if err := rendered.Chmod(0o600); err != nil {
			rendered.Close()
			os.Remove(renderedPath)
			return err
		}
		if _, err := rendered.WriteString(result.Stdout); err != nil {
			rendered.Close()
			os.Remove(renderedPath)
			return err
		}
		if err := rendered.Close(); err != nil {
			os.Remove(renderedPath)
			return err
		}
		if environment == "prod" {
			if err := (policy.Evaluator{Runner: v.Runner, ProjectRoot: v.ProjectRoot}).
				KubernetesProd(ctx, renderedPath); err != nil {
				os.Remove(renderedPath)
				return fmt.Errorf("Kubernetes policy %s: %w", root, err)
			}
		}
		if _, err := v.Runner.Run(ctx, command.Request{
			Name: "kubeconform",
			Args: []string{
				"-kubernetes-version", "1.35.4",
				"-ignore-missing-schemas",
				"-strict",
				"-summary",
				"-exit-on-error",
				"-",
			},
			Stdin:   strings.NewReader(result.Stdout),
			Timeout: 3 * time.Minute, Stream: true,
		}); err != nil {
			os.Remove(renderedPath)
			return fmt.Errorf("kubeconform %s: %w", root, err)
		}
		os.Remove(renderedPath)
	}
	return nil
}

func (v Validator) ansible(ctx context.Context, ciOnly bool) error {
	dir := filepath.Join(v.ProjectRoot, "infrastructure", "ansible")
	env := map[string]string{
		"ANSIBLE_CONFIG":     filepath.Join(dir, "ansible.cfg"),
		"ANSIBLE_ROLES_PATH": filepath.Join(dir, "roles"),
	}
	playbooks := []string{"site.yml", "post_start.yml", "backup_baseline.yml", "gitops_cicd.yml"}
	if ciOnly {
		playbooks = []string{filepath.Join("ci", "ci_runner.yml")}
	}
	for _, playbook := range playbooks {
		if err := v.run(ctx, "", env, "ansible-playbook", "--inventory", "localhost,", "--syntax-check",
			filepath.Join(dir, "playbooks", playbook)); err != nil {
			return fmt.Errorf("ansible syntax %s: %w", playbook, err)
		}
	}
	candidates, _ := filepath.Glob(filepath.Join(dir, "playbooks", "*.yml"))
	ciPlaybooks, _ := filepath.Glob(filepath.Join(dir, "playbooks", "ci", "*.yml"))
	candidates = append(candidates, ciPlaybooks...)
	roleTasks, _ := filepath.Glob(filepath.Join(dir, "roles", "*", "tasks", "*.yml"))
	candidates = append(candidates, roleTasks...)
	if err := v.run(ctx, dir, env, "ansible-lint", candidates...); err != nil {
		return fmt.Errorf("ansible-lint: %w", err)
	}
	if err := v.run(ctx, "", nil, "yamllint", "--config-file", filepath.Join(v.ProjectRoot, ".yamllint.yml"),
		dir, filepath.Join(v.ProjectRoot, "infrastructure", "k8s"), filepath.Join(v.ProjectRoot, ".github")); err != nil {
		return fmt.Errorf("yamllint: %w", err)
	}
	return nil
}

func (v Validator) run(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	_, err := v.Runner.Run(ctx, command.Request{
		Name: name, Args: args, Dir: dir, Env: env,
		Timeout: 20 * time.Minute, Stream: true,
	})
	return err
}
