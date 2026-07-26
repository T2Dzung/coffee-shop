package ci

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/policy"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type RealOperations struct {
	Config                  config.CI
	Runner                  command.Runner
	Output                  io.Writer
	AWS                     platformaws.Client
	Pricing                 platformaws.Pricing
	Policy                  policy.Evaluator
	Bootstrap               platformterraform.Client
	Terraform               platformterraform.Client
	initialBackendBootstrap bool
}

func NewRealOperations(cfg config.CI, runner command.Runner, output io.Writer) *RealOperations {
	if output == nil {
		output = io.Discard
	}
	home, _ := os.UserHomeDir()
	timeout := 30 * time.Minute
	awsClient := platformaws.Client{Runner: runner, Region: cfg.Region, Profile: cfg.AWSProfile, Timeout: timeout}
	awsEnvironment := map[string]string{}
	if cfg.AWSProfile != "" {
		awsEnvironment["AWS_PROFILE"] = cfg.AWSProfile
	}
	return &RealOperations{
		Config: cfg, Runner: runner, Output: output, AWS: awsClient,
		Pricing: platformaws.Pricing{Client: awsClient},
		Policy:  policy.Evaluator{Runner: runner, ProjectRoot: cfg.ProjectRoot},
		Bootstrap: platformterraform.Client{
			Runner: runner,
			Dir:    filepath.Join(cfg.ProjectRoot, "infrastructure", "terraform", "bootstrap", "prod"),
			DataDir: filepath.Join(home, ".cache", "go-coffeeshop", "terraform",
				"prod-bootstrap-"+cfg.AccountID),
			Variables: map[string]string{
				"aws_region": cfg.Region, "expected_aws_account_id": cfg.AccountID,
				"project_name": cfg.ProjectName,
			},
			Environment: awsEnvironment,
			Timeout:     timeout,
		},
		Terraform: platformterraform.Client{
			Runner: runner,
			Dir:    filepath.Join(cfg.ProjectRoot, "infrastructure", "terraform", "envs", "ci"),
			DataDir: filepath.Join(home, ".cache", "go-coffeeshop", "terraform",
				"ci-foundation-"+cfg.AccountID),
			VarFile:     cfg.VarFile,
			Environment: awsEnvironment,
			Timeout:     timeout,
		},
	}
}

func (o *RealOperations) Preflight(ctx context.Context, action Action) error {
	tools := [][]string{{"terraform", "version"}, {"aws", "--version"}}
	if action == ActionSetup {
		tools = append(tools, []string{o.ansiblePlaybook(), "--version"}, []string{"ssh", "-V"})
	} else if action == ActionStatus {
		tools = append(tools, []string{"ssh", "-V"})
	}
	for _, tool := range tools {
		if _, err := o.Runner.Run(ctx, command.Request{
			Name: tool[0], Args: tool[1:], Timeout: 30 * time.Second,
		}); err != nil {
			return fmt.Errorf("required tool %s: %w", tool[0], err)
		}
	}
	account, err := o.AWS.Text(ctx, "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	if err != nil {
		return err
	}
	if account != o.Config.AccountID {
		return fmt.Errorf("active AWS account %s does not match CI account %s", account, o.Config.AccountID)
	}
	if action == ActionSetup {
		if err := o.validateSetupSecrets(); err != nil {
			return err
		}
		estimate, err := o.Pricing.EstimateCI(ctx, platformaws.CIEstimateInput{
			Region: o.Config.Region, InstanceType: o.Config.InstanceType, DiskGiB: o.Config.RootVolumeGiB,
		})
		if err != nil {
			return fmt.Errorf("dynamic CI hourly estimate: %w", err)
		}
		fmt.Fprintf(o.Output,
			"Estimated fixed CI cost/hour: instance USD %.4f + gp3 USD %.4f + public IPv4 USD %.4f = USD %.4f\n",
			estimate.Instance, estimate.EBS, estimate.PublicIPv4, estimate.Total())
		fmt.Fprintln(o.Output, "Traffic, registry storage/API usage and taxes are usage-priced and excluded.")
	} else if action == ActionStatus {
		if err := o.validateSSHKey(); err != nil {
			return err
		}
	}
	return nil
}

func (o *RealOperations) validateSetupSecrets() error {
	if o.Config.KeyName == "" || len(o.Config.OperatorSSHCIDRs) == 0 {
		return fmt.Errorf("SSH-based setup requires key_name and at least one operator_ssh_cidrs entry in CI tfvars")
	}
	if err := o.validateSSHKey(); err != nil {
		return err
	}
	if o.Config.GitHubAuthMode == "github_app" &&
		(o.Config.GitHubAppID == "" || o.Config.GitHubAppInstallationID == "" || o.Config.GitHubAppPrivateKey == "") {
		return fmt.Errorf("GitHub App mode requires ARC_GITHUB_APP_ID, ARC_GITHUB_APP_INSTALLATION_ID and ARC_GITHUB_APP_PRIVATE_KEY")
	}
	if o.Config.GitHubAuthMode == "pat" && o.Config.GitHubToken == "" {
		return fmt.Errorf("PAT mode requires ARC_GITHUB_TOKEN")
	}
	return nil
}

func (o *RealOperations) validateSSHKey() error {
	if o.Config.SSHPrivateKey == "" {
		return fmt.Errorf("CI_SSH_PRIVATE_KEY is required for the current SSH-based Ansible adapter")
	}
	if info, err := os.Stat(o.Config.SSHPrivateKey); err != nil || info.IsDir() {
		return fmt.Errorf("CI_SSH_PRIVATE_KEY does not identify a readable private-key file")
	} else if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("CI_SSH_PRIVATE_KEY must have mode 0600")
	}
	if strings.HasPrefix(o.Config.SSHPrivateKey, "/mnt/") {
		return fmt.Errorf("CI_SSH_PRIVATE_KEY must be stored on the WSL filesystem, not under /mnt")
	}
	return nil
}

func (o *RealOperations) Plan(ctx context.Context, action Action) (Plan, error) {
	backendExists, err := o.backendRoleExists(ctx)
	if err != nil {
		return Plan{}, err
	}
	parts := make([]PlanPart, 0, 2)
	if action == ActionSetup {
		if err := o.initBootstrap(ctx); err != nil {
			return Plan{}, err
		}
		bootstrapArtifact, err := o.Bootstrap.CreatePlan(ctx, "", "ci-backend-boundary", false, nil)
		if err != nil {
			return Plan{}, err
		}
		if err := o.Policy.Terraform(ctx, "reconcile", bootstrapArtifact.JSONPath); err != nil {
			bootstrapArtifact.Cleanup()
			return Plan{}, err
		}
		bootstrapHuman, err := o.Bootstrap.ShowHuman(ctx, bootstrapArtifact)
		if err != nil {
			bootstrapArtifact.Cleanup()
			return Plan{}, err
		}
		parts = append(parts, PlanPart{
			Name: "retained backend boundary", Artifact: bootstrapArtifact, Human: bootstrapHuman,
		})
		o.initialBackendBootstrap = !backendExists
	} else if !backendExists {
		return Plan{}, fmt.Errorf("CI backend role is absent; teardown will not create retained resources")
	}
	if err := o.initRemote(ctx, !o.initialBackendBootstrap); err != nil {
		Plan{Parts: parts}.Cleanup()
		return Plan{}, err
	}
	artifact, err := o.Terraform.CreatePlan(ctx, "", "ci-"+string(action), action == ActionTeardown, nil)
	if err != nil {
		Plan{Parts: parts}.Cleanup()
		return Plan{}, err
	}
	policyName := "ci-reconcile"
	if action == ActionTeardown {
		policyName = "ci-teardown"
	}
	if err := o.Policy.Terraform(ctx, policyName, artifact.JSONPath); err != nil {
		Plan{Parts: parts}.Cleanup()
		artifact.Cleanup()
		return Plan{}, err
	}
	human, err := o.Terraform.ShowHuman(ctx, artifact)
	if err != nil {
		Plan{Parts: parts}.Cleanup()
		artifact.Cleanup()
		return Plan{}, err
	}
	parts = append(parts, PlanPart{Name: "CI foundation", Artifact: artifact, Human: human})
	return Plan{Parts: parts}, nil
}

func (o *RealOperations) initRemote(ctx context.Context, assumeScopedRole bool) error {
	kmsARN, err := o.AWS.Text(ctx, "kms", "describe-key", "--key-id", o.Config.StateKMSKeyID,
		"--query", "KeyMetadata.Arn", "--output", "text")
	if err != nil {
		return err
	}
	args := []string{
		"-backend-config=bucket=" + o.Config.StateBucket,
		"-backend-config=key=" + o.Config.StateKey,
		"-backend-config=region=" + o.Config.Region,
		"-backend-config=encrypt=true",
		"-backend-config=kms_key_id=" + kmsARN,
		"-backend-config=use_lockfile=true",
	}
	if assumeScopedRole && o.Config.BackendRoleARN != "" {
		args = append(args, "-backend-config=role_arn="+o.Config.BackendRoleARN)
	}
	return o.Terraform.Init(ctx, args...)
}

func (o *RealOperations) initBootstrap(ctx context.Context) error {
	kmsARN, err := o.AWS.Text(ctx, "kms", "describe-key", "--key-id", o.Config.StateKMSKeyID,
		"--query", "KeyMetadata.Arn", "--output", "text")
	if err != nil {
		return err
	}
	prodRole := "arn:aws:iam::" + o.Config.AccountID + ":role/" + o.Config.ProjectName + "-terraform-backend-role"
	return o.Bootstrap.Init(ctx,
		"-backend-config=bucket="+o.Config.StateBucket,
		"-backend-config=key=prod/bootstrap.tfstate",
		"-backend-config=region="+o.Config.Region,
		"-backend-config=encrypt=true",
		"-backend-config=kms_key_id="+kmsARN,
		"-backend-config=use_lockfile=true",
		"-backend-config=role_arn="+prodRole,
	)
}

func (o *RealOperations) backendRoleExists(ctx context.Context) (bool, error) {
	result, err := o.Runner.Run(ctx, command.Request{
		Name:    "aws",
		Args:    []string{"iam", "get-role", "--role-name", o.Config.ProjectName + "-ci-terraform-backend-role"},
		Env:     o.awsEnv(),
		Timeout: time.Minute,
	})
	if err == nil {
		return true, nil
	}
	if strings.Contains(result.Stderr, "NoSuchEntity") {
		return false, nil
	}
	return false, fmt.Errorf("inspect CI backend role: %w", err)
}

func (o *RealOperations) Apply(ctx context.Context, plan Plan) error {
	for _, part := range plan.Parts {
		if part.Artifact.Summary == (platformterraform.Summary{}) {
			continue
		}
		client := o.Terraform
		if part.Name == "retained backend boundary" {
			client = o.Bootstrap
		}
		if err := client.Apply(ctx, part.Artifact); err != nil {
			return fmt.Errorf("apply %s: %w", part.Name, err)
		}
	}
	if o.initialBackendBootstrap {
		return o.initRemote(ctx, true)
	}
	return nil
}

func (o *RealOperations) Configure(ctx context.Context) error {
	instanceID, err := o.Terraform.Output(ctx, "instance_id")
	if err != nil {
		return err
	}
	instanceID = strings.TrimSpace(instanceID)
	if err := o.AWS.Run(ctx, "ec2", "wait", "instance-status-ok", "--instance-ids", instanceID); err != nil {
		return err
	}
	publicIP, err := o.Terraform.Output(ctx, "public_ip")
	if err != nil {
		return err
	}
	if err := o.waitForSSH(ctx, strings.TrimSpace(publicIP)); err != nil {
		return err
	}
	ansibleDir := filepath.Join(o.Config.ProjectRoot, "infrastructure", "ansible")
	redactions := []string{o.Config.GitHubAppPrivateKey, o.Config.GitHubToken}
	environment := map[string]string{
		"ANSIBLE_CONFIG":                 filepath.Join(ansibleDir, "ansible.cfg"),
		"ANSIBLE_ROLES_PATH":             filepath.Join(ansibleDir, "roles"),
		"AWS_REGION":                     o.Config.Region,
		"AWS_DEFAULT_REGION":             o.Config.Region,
		"ARC_GITHUB_CONFIG_URL":          "https://github.com/" + o.Config.GitHubRepository,
		"ARC_GITHUB_AUTH_MODE":           o.Config.GitHubAuthMode,
		"ARC_GITHUB_APP_ID":              o.Config.GitHubAppID,
		"ARC_GITHUB_APP_INSTALLATION_ID": o.Config.GitHubAppInstallationID,
		"ARC_GITHUB_APP_PRIVATE_KEY":     o.Config.GitHubAppPrivateKey,
		"ARC_GITHUB_TOKEN":               o.Config.GitHubToken,
		"ARC_MAX_RUNNERS":                strconv.Itoa(o.Config.MaxRunners),
	}
	if o.Config.AWSProfile != "" {
		environment["AWS_PROFILE"] = o.Config.AWSProfile
	}
	_, err = o.Runner.Run(ctx, command.Request{
		Name: o.ansiblePlaybook(),
		Args: []string{
			"--inventory", filepath.Join(ansibleDir, "inventory", "ci_aws_ec2.yml"),
			"--private-key", o.Config.SSHPrivateKey,
			filepath.Join(ansibleDir, "playbooks", "ci_runner.yml"),
		},
		Dir:     ansibleDir,
		Env:     environment,
		Timeout: 45 * time.Minute, Stream: true, Redactions: redactions,
	})
	return err
}

func (o *RealOperations) waitForSSH(ctx context.Context, host string) error {
	for attempt := 1; attempt <= 30; attempt++ {
		_, err := o.ssh(ctx, host, "true")
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return fmt.Errorf("SSH did not become ready on CI host %s", host)
}

func (o *RealOperations) Verify(ctx context.Context, action Action) error {
	if action == ActionTeardown {
		return o.verifyTeardown(ctx)
	}
	if err := o.initRemote(ctx, true); err != nil {
		return err
	}
	buildRole, err := o.Terraform.Output(ctx, "candidate_build_role_arn")
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Output, "GitHub ci-build secret CI_AWS_ROLE_ARN=%s\n", strings.TrimSpace(buildRole))
	fmt.Fprintf(o.Output, "GitHub ci-build variable CI_AWS_REGION=%s\n", o.Config.Region)
	host, err := o.Terraform.Output(ctx, "public_ip")
	if err != nil {
		return err
	}
	commandText := strings.Join([]string{
		"sudo systemctl is-active --quiet k3s",
		"sudo k3s kubectl wait --for=condition=Ready node --all --timeout=120s",
		"sudo k3s kubectl get autoscalingrunnersets.actions.github.com trusted-build -n ci-runners",
		"sudo k3s kubectl get pods -n arc-systems",
	}, " && ")
	result, err := o.ssh(ctx, strings.TrimSpace(host), commandText)
	if err != nil {
		return err
	}
	fmt.Fprint(o.Output, result.Stdout)
	fmt.Fprintln(o.Output, "CI status passed: EC2, K3s, ARC controller and trusted-build scale set are reachable.")
	return nil
}

func (o *RealOperations) verifyTeardown(ctx context.Context) error {
	queries := [][]string{
		{"ec2", "describe-instances", "--filters", "Name=tag:Environment,Values=ci", "Name=instance-state-name,Values=pending,running,stopping,stopped", "--query", "length(Reservations[].Instances[])", "--output", "text"},
		{"ec2", "describe-vpcs", "--filters", "Name=tag:Environment,Values=ci", "--query", "length(Vpcs)", "--output", "text"},
	}
	for _, query := range queries {
		value, err := o.AWS.Text(ctx, query...)
		if err != nil {
			return err
		}
		if value != "0" {
			return fmt.Errorf("CI teardown inventory still contains resources for query %v", query[:2])
		}
	}
	for _, role := range []string{o.Config.ProjectName + "-ci-runner-host", o.Config.ProjectName + "-ci-candidate-build"} {
		result, err := o.Runner.Run(ctx, command.Request{
			Name: "aws", Args: []string{"iam", "get-role", "--role-name", role},
			Env:     o.awsEnv(),
			Timeout: time.Minute,
		})
		if err == nil {
			return fmt.Errorf("CI teardown inventory still contains IAM role %s", role)
		}
		if !strings.Contains(result.Stderr, "NoSuchEntity") {
			return fmt.Errorf("verify absence of CI IAM role %s: %w", role, err)
		}
	}
	fmt.Fprintln(o.Output, "CI teardown passed: no CI-tagged instance/VPC or CI IAM role remains; backend state is retained.")
	return nil
}

func (o *RealOperations) awsEnv() map[string]string {
	environment := map[string]string{"AWS_REGION": o.Config.Region, "AWS_DEFAULT_REGION": o.Config.Region}
	if o.Config.AWSProfile != "" {
		environment["AWS_PROFILE"] = o.Config.AWSProfile
	}
	return environment
}

func (o *RealOperations) ansiblePlaybook() string {
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".venvs", "go-coffeeshop-platform", "bin", "ansible-playbook")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return "ansible-playbook"
}

func (o *RealOperations) ssh(ctx context.Context, host, remoteCommand string) (command.Result, error) {
	return o.Runner.Run(ctx, command.Request{
		Name: "ssh",
		Args: []string{
			"-i", o.Config.SSHPrivateKey,
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=10",
			"ubuntu@" + host,
			remoteCommand,
		},
		Timeout: 3 * time.Minute,
	})
}
