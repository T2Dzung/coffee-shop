package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	platformci "github.com/thangchung/go-coffeeshop/internal/platformctl/ci"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	platformdev "github.com/thangchung/go-coffeeshop/internal/platformctl/dev"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	platformgithub "github.com/thangchung/go-coffeeshop/internal/platformctl/github"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/policy"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/prod"
	releasepolicy "github.com/thangchung/go-coffeeshop/internal/platformctl/release"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/toolchain"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/validation"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "platformctl:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runContext(context.Background(), args, stdin, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	operatorConfig, args, err := parseGlobalOptions(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl [--operator-config path] <prod|dev|ci|github|config|component|toolchain|validate|terraform-plan|release|version> ...")
	}
	switch args[0] {
	case "prod":
		return runProd(ctx, args[1:], stdin, stdout, stderr, operatorConfig)
	case "ci":
		return runCI(ctx, args[1:], stdin, stdout, stderr, operatorConfig)
	case "dev":
		return runDev(ctx, args[1:], stdin, stdout, stderr, operatorConfig)
	case "github":
		return runGitHub(ctx, args[1:], stdin, stdout, stderr, operatorConfig)
	case "config":
		return runConfig(args[1:], stdout, stderr, operatorConfig)
	case "component":
		return runComponent(args[1:], stdout, stderr)
	case "toolchain":
		return runToolchain(args[1:], stdout, stderr)
	case "validate":
		return runValidate(ctx, args[1:], stdout, stderr)
	case "terraform-plan":
		return runTerraformPlan(ctx, args[1:], stdout, stderr)
	case "release":
		return runRelease(args[1:], stdout)
	case "version":
		_, err := fmt.Fprintln(stdout, "platformctl 0.4.0-dev")
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runGitHub(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, operatorConfig string) error {
	if len(args) == 0 || (args[0] != "bootstrap" && args[0] != "doctor") {
		return fmt.Errorf("usage: platformctl github <bootstrap|doctor> [--auto-approve]")
	}
	flags := flag.NewFlagSet("github "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	autoApprove := flags.Bool("auto-approve", false, "explicitly disable interactive approval")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	loader := config.NewLoader()
	loader.OperatorConfigPath = operatorConfig
	cfg, err := loader.LoadGitHub(root)
	if err != nil {
		return err
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	operations, err := platformgithub.NewRealOperations(cfg, runner)
	if err != nil {
		return err
	}
	defer operations.Close()
	engine := platformgithub.Engine{
		Operations: operations,
		Approver: platformgithub.ConsoleApprover{
			Input: stdin, Output: stdout, AutoApprove: *autoApprove,
		},
		Secrets: cfg.RepositorySecretData,
	}
	if args[0] == "doctor" {
		if err := engine.Doctor(ctx); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "GitHub governance doctor passed.")
		return nil
	}
	if err := engine.Bootstrap(ctx); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "GitHub governance bootstrap passed.")
	return nil
}

func runDev(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, operatorConfig string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl dev <setup|status|teardown> [flags]")
	}
	action := platformdev.Action(args[0])
	if !action.Valid() {
		return fmt.Errorf("unsupported DEV action %q", action)
	}
	flags := flag.NewFlagSet("dev "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)
	varFile := flags.String("var-file", "", "DEV Terraform tfvars file")
	autoApprove := flags.Bool("auto-approve", false, "explicitly disable interactive approval")
	evidencePath := flags.String("evidence", "", "structured evidence JSON path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	loader := config.NewLoader()
	loader.OperatorConfigPath = operatorConfig
	cfg, err := loader.LoadDev(root, *varFile)
	if err != nil {
		return err
	}
	if *autoApprove {
		cfg.AutoApprove = true
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	approver := platformdev.ConsoleApprover{Input: stdin, Output: stdout, AutoApprove: cfg.AutoApprove}
	recorder := evidence.New("dev-" + string(action))
	runErr := (platformdev.Engine{
		Operations: platformdev.NewRealOperations(cfg, runner, stdout), Approver: approver, Evidence: recorder,
	}).Run(ctx, action)
	if *evidencePath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr == nil {
			*evidencePath = filepath.Join(home, "coffeeshop-evidence", "platformctl",
				time.Now().UTC().Format("20060102T150405Z")+"-dev-"+string(action)+".json")
		}
	}
	if *evidencePath != "" {
		if err := evidence.WriteAtomic(*evidencePath, recorder.Snapshot()); err != nil && runErr == nil {
			return err
		}
		fmt.Fprintln(stdout, "Evidence:", *evidencePath)
	}
	return runErr
}

func parseGlobalOptions(args []string) (string, []string, error) {
	if len(args) == 0 || args[0] != "--operator-config" {
		return "", args, nil
	}
	if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
		return "", nil, fmt.Errorf("--operator-config requires a path")
	}
	return args[1], args[2:], nil
}

func runConfig(args []string, stdout, stderr io.Writer, operatorConfig string) error {
	if len(args) == 0 || args[0] != "doctor" {
		return fmt.Errorf("usage: platformctl [--operator-config path] config doctor --environment <dev|ci|prod|all>")
	}
	flags := flag.NewFlagSet("config doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	environment := flags.String("environment", "all", "dev, ci, prod or all")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	report, err := (config.Doctor{
		Loader: config.Loader{OperatorConfigPath: operatorConfig}, ProjectRoot: root,
	}).Run(*environment)
	if err != nil {
		return err
	}
	return writeJSON(stdout, report)
}

func runToolchain(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl toolchain <describe|verify> [flags]")
	}
	flags := flag.NewFlagSet("toolchain "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	name := flags.String("name", "", "tool name")
	profile := flags.String("profile", "", "toolchain execution profile")
	catalogPath := flags.String("catalog", "platform/toolchain.yaml", "toolchain catalog")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	catalog, err := toolchain.Load(*catalogPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "describe":
		tool, err := catalog.Find(*name)
		if err != nil {
			return err
		}
		return writeJSON(stdout, tool)
	case "verify":
		if *profile == "" {
			return fmt.Errorf("--profile is required")
		}
		if err := catalog.VerifyProfile(*profile, exec.LookPath); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]string{"profile": *profile, "status": "ready"})
	default:
		return fmt.Errorf("unsupported toolchain action %q", args[0])
	}
}

func runComponent(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl component <describe|list|select|validate-paths|resolve|candidate-repositories> [flags]")
	}
	flags := flag.NewFlagSet("component "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	catalogPath := flags.String("catalog", "platform/components.yaml", "component catalog")
	name := flags.String("name", "", "component name")
	changedFilesPath := flags.String("changed-files", "", "newline-delimited changed files")
	names := flags.String("names", "", "comma-separated component names")
	allowMigration := flags.Bool("allow-migration", false, "acknowledge stateful migration boundary")
	kind := flags.String("kind", "", "optional component kind filter")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	catalog, err := component.Load(*catalogPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "describe":
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		entry, err := catalog.Find(*name)
		if err != nil {
			return err
		}
		return writeJSON(stdout, entry)
	case "list":
		return writeJSON(stdout, catalog.FilterKind(catalog.Names(), *kind))
	case "select":
		if *changedFilesPath == "" {
			return fmt.Errorf("--changed-files is required")
		}
		data, err := os.ReadFile(*changedFilesPath)
		if err != nil {
			return fmt.Errorf("read changed files: %w", err)
		}
		return writeJSON(stdout, catalog.FilterKind(catalog.Select(strings.Fields(string(data))), *kind))
	case "validate-paths":
		if *name == "" {
			return fmt.Errorf("--name is required")
		}
		if *changedFilesPath == "" {
			return fmt.Errorf("--changed-files is required")
		}
		data, err := os.ReadFile(*changedFilesPath)
		if err != nil {
			return fmt.Errorf("read changed files: %w", err)
		}
		if err := catalog.ValidateChangedFiles(*name, strings.Fields(string(data))); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]string{"component": *name, "status": "valid"})
	case "resolve":
		resolved, err := catalog.Resolve(strings.Split(*names, ","), *allowMigration)
		if err != nil {
			return err
		}
		return writeJSON(stdout, resolved)
	case "candidate-repositories":
		if strings.TrimSpace(*names) == "" {
			return fmt.Errorf("--names is required")
		}
		repositories, err := catalog.CandidateRepositoryNames(strings.Split(*names, ","))
		if err != nil {
			return err
		}
		return writeJSON(stdout, repositories)
	default:
		return fmt.Errorf("unsupported component action %q", args[0])
	}
}

func runCI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, operatorConfig string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl ci <setup|status|teardown> [flags]")
	}
	action := platformci.Action(args[0])
	if !action.Valid() {
		return fmt.Errorf("unsupported CI action %q", action)
	}
	flags := flag.NewFlagSet("ci "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)
	varFile := flags.String("var-file", "", "CI Terraform tfvars file")
	autoApprove := flags.Bool("auto-approve", false, "explicitly disable interactive approval")
	evidencePath := flags.String("evidence", "", "structured evidence JSON path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	loader := config.NewLoader()
	loader.OperatorConfigPath = operatorConfig
	cfg, err := loader.LoadCI(root, *varFile)
	if err != nil {
		return err
	}
	if *autoApprove {
		cfg.AutoApprove = true
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	approver := platformci.ConsoleApprover{Input: stdin, Output: stdout, AutoApprove: cfg.AutoApprove}
	recorder := evidence.New("ci-" + string(action))
	runErr := (platformci.Engine{
		Operations: platformci.NewRealOperations(cfg, runner, stdout),
		Approver:   approver,
		Evidence:   recorder,
	}).Run(ctx, action)
	if *evidencePath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr == nil {
			*evidencePath = filepath.Join(home, "coffeeshop-evidence", "platformctl",
				time.Now().UTC().Format("20060102T150405Z")+"-ci-"+string(action)+".json")
		}
	}
	if *evidencePath != "" {
		if err := evidence.WriteAtomic(*evidencePath, recorder.Snapshot()); err != nil && runErr == nil {
			return err
		}
		fmt.Fprintln(stdout, "Evidence:", *evidencePath)
	}
	return runErr
}

func runProd(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, operatorConfig string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl prod <setup|reconcile|status|resilience|teardown> [flags]")
	}
	action := prod.Action(args[0])
	if !action.Valid() {
		return fmt.Errorf("unsupported PROD action %q", action)
	}
	flags := flag.NewFlagSet("prod "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)
	varFile := flags.String("var-file", "", "PROD Terraform tfvars file")
	autoApprove := flags.Bool("auto-approve", false, "explicitly disable interactive approval")
	evidencePath := flags.String("evidence", "", "structured evidence JSON path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	loader := config.NewLoader()
	loader.OperatorConfigPath = operatorConfig
	cfg, err := loader.LoadProd(root, *varFile)
	if err != nil {
		return err
	}
	if *autoApprove {
		cfg.AutoApprove = true
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	approver := prod.ConsoleApprover{Input: stdin, Output: stdout, AutoApprove: cfg.AutoApprove}
	recorder := evidence.New("prod-" + string(action))
	operations := prod.NewRealOperations(cfg, runner, approver, stdout)
	runErr := (prod.Engine{
		Operations: operations, Approver: approver, Evidence: recorder,
	}).Run(ctx, action)
	bundle := recorder.Snapshot()
	if *evidencePath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr == nil {
			*evidencePath = filepath.Join(home, "coffeeshop-evidence", "platformctl",
				time.Now().UTC().Format("20060102T150405Z")+"-prod-"+string(action)+".json")
		}
	}
	if *evidencePath != "" {
		if err := evidence.WriteAtomic(*evidencePath, bundle); err != nil && runErr == nil {
			return err
		}
		fmt.Fprintln(stdout, "Evidence:", *evidencePath)
	}
	return runErr
}

func runValidate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profile := flags.String("profile", "all", "all, dev, prod or ci")
	envAlias := flags.String("env", "", "compatibility alias for --profile")
	scope := flags.String("scope", "all", "all, terraform or platform")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *envAlias != "" {
		*profile = *envAlias
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	if err := (validation.Validator{
		Runner: runner, ProjectRoot: root, Profile: validation.Profile(*profile),
		Scope: validation.Scope(*scope),
	}).Run(ctx); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Validation passed (profile=%s scope=%s).\n", *profile, *scope)
	return nil
}

func runTerraformPlan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("terraform-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "Terraform plan JSON file")
	policy := flags.String("policy", "", "reconcile or teardown")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return fmt.Errorf("--input is required")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	runner := command.OSRunner{Stdout: stdout, Stderr: stderr}
	if err := (policyEvaluator(runner, root)).Terraform(ctx, *policy, *input); err != nil {
		return err
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	summary, err := platformterraform.DecodeSummary(data)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"policy": *policy, "delete_count": summary.Delete + summary.Replace,
		"create_count": summary.Create + summary.Replace, "update_count": summary.Update,
	})
}

func policyEvaluator(runner command.Runner, root string) policy.Evaluator {
	return policy.Evaluator{Runner: runner, ProjectRoot: root}
}

func runRelease(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: platformctl release <identity|request|candidate|dev|standard|rollback|manifest> ...")
	}
	switch args[0] {
	case "identity":
		flags := flag.NewFlagSet("release identity", flag.ContinueOnError)
		lane := flags.String("lane", "", "standard, emergency or rollback")
		service := flags.String("service", "", "service name")
		commit := flags.String("source-commit", "", "source commit")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		if err := releasepolicy.ValidateIdentity(*lane, *service, *commit); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]string{
			"lane": *lane, "service": *service, "source_commit": *commit,
		})
	case "request":
		flags := flag.NewFlagSet("release request", flag.ContinueOnError)
		requestPath := flags.String("request", "", "release request JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		request, err := releasepolicy.ValidateRequest(*requestPath)
		if err != nil {
			return err
		}
		for _, service := range request.Components {
			if err := validateReleaseComponent(service); err != nil {
				return err
			}
		}
		return writeJSON(stdout, request)
	case "standard":
		flags := flag.NewFlagSet("release standard", flag.ContinueOnError)
		service := flags.String("service", "", "service name")
		commit := flags.String("source-commit", "", "source commit")
		candidate := flags.String("candidate", "", "candidate JSON")
		qa := flags.String("qa", "", "QA JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		artifact, err := releasepolicy.ValidateStandard(*service, *commit, *candidate, *qa)
		if err != nil {
			return err
		}
		return writeJSON(stdout, artifact)
	case "candidate":
		flags := flag.NewFlagSet("release candidate", flag.ContinueOnError)
		service := flags.String("service", "", "component name")
		commit := flags.String("source-commit", "", "source commit")
		candidate := flags.String("candidate", "", "candidate JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		artifact, err := releasepolicy.ValidateCandidate(*service, *commit, *candidate)
		if err != nil {
			return err
		}
		return writeJSON(stdout, artifact)
	case "dev":
		flags := flag.NewFlagSet("release dev", flag.ContinueOnError)
		service := flags.String("service", "", "component name")
		commit := flags.String("source-commit", "", "source commit")
		candidate := flags.String("candidate", "", "candidate JSON")
		devRelease := flags.String("dev-release", "", "DEV release JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		release, err := releasepolicy.ValidateDevRelease(*service, *commit, *candidate, *devRelease)
		if err != nil {
			return err
		}
		return writeJSON(stdout, release)
	case "rollback":
		flags := flag.NewFlagSet("release rollback", flag.ContinueOnError)
		service := flags.String("service", "", "service name")
		commit := flags.String("source-commit", "", "released source commit")
		history := flags.String("history", "", "release history JSON")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		artifact, err := releasepolicy.ValidateRollback(*service, *commit, *history)
		if err != nil {
			return err
		}
		return writeJSON(stdout, artifact)
	case "manifest":
		flags := flag.NewFlagSet("release manifest", flag.ContinueOnError)
		lane := flags.String("lane", "", "standard, emergency or rollback")
		service := flags.String("service", "", "service name")
		commit := flags.String("source-commit", "", "source commit")
		image := flags.String("image", "", "PROD image")
		digest := flags.String("digest", "", "PROD digest")
		baseline := flags.String("baseline", "", "optional PROD baseline commit")
		workflowRun := flags.String("workflow-run", "", "workflow run URL")
		recordedAt := flags.String("recorded-at", "", "RFC3339 timestamp")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateReleaseComponent(*service); err != nil {
			return err
		}
		manifest, err := releasepolicy.NewManifest(
			*lane, *service, *commit, *image, *digest, *baseline, *workflowRun, *recordedAt,
		)
		if err != nil {
			return err
		}
		return writeJSON(stdout, manifest)
	default:
		return fmt.Errorf("unknown release command %q", args[0])
	}
}

func validateReleaseComponent(name string) error {
	catalog, err := component.Load(filepath.Join("platform", "components.yaml"))
	if err != nil {
		return err
	}
	entry, err := catalog.Find(name)
	if err != nil {
		return err
	}
	if entry.Kind == "migration" {
		return fmt.Errorf("migration component %q requires its dedicated stateful release flow", name)
	}
	return nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
