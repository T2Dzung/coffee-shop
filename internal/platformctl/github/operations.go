package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

var RequiredRepositoryVariables = []string{
	"CI_AWS_REGION", "CI_AWS_ROLE_ARN", "DEV_AWS_REGION", "DEV_AWS_ROLE_ARN",
	"DEV_CANDIDATE_READER_ROLE_ARN", "PROD_AWS_REGION", "PROD_AWS_ROLE_ARN",
	"PROD_EMERGENCY_AWS_ROLE_ARN", "TRUSTED_BUILD_RUNNER",
}

type RealOperations struct {
	Config    config.GitHub
	Runner    command.Runner
	AWS       platformaws.Client
	Terraform platformterraform.Client
	dataDir   string
}

func NewRealOperations(cfg config.GitHub, runner command.Runner) (*RealOperations, error) {
	dataDir, err := os.MkdirTemp("", "platformctl-github-")
	if err != nil {
		return nil, fmt.Errorf("create GitHub Terraform data directory: %w", err)
	}
	environment := map[string]string{"GITHUB_TOKEN": cfg.GovernanceToken}
	if cfg.AWSProfile != "" {
		environment["AWS_PROFILE"] = cfg.AWSProfile
	}
	timeout := 20 * time.Minute
	return &RealOperations{
		Config: cfg, Runner: runner, dataDir: dataDir,
		AWS: platformaws.Client{
			Runner: runner, Region: cfg.Region, Profile: cfg.AWSProfile, Timeout: timeout,
		},
		Terraform: platformterraform.Client{
			Runner: runner, Dir: cfg.TerraformDir, DataDir: dataDir, VarFile: cfg.VarFile,
			Environment: environment,
			Redactions:  []string{cfg.GovernanceToken}, Timeout: timeout,
		},
	}, nil
}

func (o *RealOperations) Close() error {
	return os.RemoveAll(o.dataDir)
}

func (o *RealOperations) Plan(ctx context.Context) (Plan, error) {
	for _, tool := range [][]string{{"terraform", "version"}, {"aws", "--version"}, {"gh", "--version"}} {
		if _, err := o.Runner.Run(ctx, command.Request{
			Name: tool[0], Args: tool[1:], Timeout: 30 * time.Second,
		}); err != nil {
			return Plan{}, fmt.Errorf("required tool %s: %w", tool[0], err)
		}
	}
	account, err := o.AWS.Text(ctx, "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	if err != nil {
		return Plan{}, fmt.Errorf("resolve GitHub governance AWS identity: %w", err)
	}
	if account != o.Config.AccountID {
		return Plan{}, fmt.Errorf(
			"active AWS account %s does not match GitHub governance state account %s",
			account, o.Config.AccountID,
		)
	}
	kmsARN, err := o.AWS.Text(ctx, "kms", "describe-key", "--key-id", o.Config.StateKMSKeyID,
		"--query", "KeyMetadata.Arn", "--output", "text")
	if err != nil {
		return Plan{}, fmt.Errorf("resolve GitHub governance state KMS key: %w", err)
	}
	if err := o.Terraform.InitS3(ctx, platformterraform.S3BackendConfig{
		Bucket: o.Config.StateBucket, Key: o.Config.StateKey, Region: o.Config.Region,
		KMSKeyARN: kmsARN, RoleARN: o.Config.BackendRoleARN, Encrypt: true, UseLockfile: true,
	}); err != nil {
		return Plan{}, fmt.Errorf("initialize GitHub governance: %w", err)
	}
	if err := o.Terraform.Validate(ctx); err != nil {
		return Plan{}, fmt.Errorf("validate GitHub governance: %w", err)
	}
	artifact, err := o.Terraform.CreatePlan(ctx, "", "github-governance", false, nil)
	if err != nil {
		return Plan{}, fmt.Errorf("plan GitHub governance: %w", err)
	}
	human, err := o.Terraform.ShowHuman(ctx, artifact)
	if err != nil {
		_ = artifact.Cleanup()
		return Plan{}, fmt.Errorf("show GitHub governance plan: %w", err)
	}
	return Plan{Artifact: artifact, Human: human}, nil
}

func (o *RealOperations) Apply(ctx context.Context, plan Plan) error {
	return o.Terraform.Apply(ctx, plan.Artifact)
}

func (o *RealOperations) ExistingSecrets(ctx context.Context) (map[string]struct{}, error) {
	names, err := o.listNames(ctx, "secret")
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result, nil
}

func (o *RealOperations) SetSecret(ctx context.Context, name, value string) error {
	_, err := o.Runner.Run(ctx, command.Request{
		Name: "gh", Args: []string{"secret", "set", name, "--repo", o.fullName()},
		Env:   map[string]string{"GH_TOKEN": o.Config.GovernanceToken},
		Stdin: strings.NewReader(value), Timeout: time.Minute,
		Redactions: []string{o.Config.GovernanceToken, value},
	})
	return err
}

func (o *RealOperations) Verify(ctx context.Context) error {
	secrets, err := o.listNames(ctx, "secret")
	if err != nil {
		return err
	}
	variables, err := o.listNames(ctx, "variable")
	if err != nil {
		return err
	}
	if missing := missingNames(RequiredRepositorySecrets, secrets); len(missing) > 0 {
		return fmt.Errorf("missing repository secrets: %s", strings.Join(missing, ", "))
	}
	if missing := missingNames(RequiredRepositoryVariables, variables); len(missing) > 0 {
		return fmt.Errorf("missing repository variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (o *RealOperations) listNames(ctx context.Context, kind string) ([]string, error) {
	result, err := o.Runner.Run(ctx, command.Request{
		Name: "gh", Args: []string{kind, "list", "--repo", o.fullName(), "--json", "name"},
		Env:     map[string]string{"GH_TOKEN": o.Config.GovernanceToken},
		Timeout: time.Minute, Redactions: []string{o.Config.GovernanceToken},
	})
	if err != nil {
		return nil, fmt.Errorf("list GitHub %ss: %w", kind, err)
	}
	var items []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &items); err != nil {
		return nil, fmt.Errorf("decode GitHub %s list: %w", kind, err)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (o *RealOperations) fullName() string {
	return o.Config.Owner + "/" + o.Config.Repository
}

func missingNames(required, actual []string) []string {
	present := make(map[string]struct{}, len(actual))
	for _, name := range actual {
		present[name] = struct{}{}
	}
	var missing []string
	for _, name := range required {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}
