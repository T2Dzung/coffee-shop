package dev

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/policy"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

type RealOperations struct {
	Config    config.Dev
	Runner    command.Runner
	Output    io.Writer
	AWS       platformaws.Client
	Policy    policy.Evaluator
	Terraform platformterraform.Client
}

func NewRealOperations(cfg config.Dev, runner command.Runner, output io.Writer) *RealOperations {
	if output == nil {
		output = io.Discard
	}
	home, _ := os.UserHomeDir()
	awsEnvironment := map[string]string{}
	if cfg.AWSProfile != "" {
		awsEnvironment["AWS_PROFILE"] = cfg.AWSProfile
	}
	timeout := 45 * time.Minute
	return &RealOperations{
		Config: cfg, Runner: runner, Output: output,
		AWS:    platformaws.Client{Runner: runner, Region: cfg.Region, Profile: cfg.AWSProfile, Timeout: timeout},
		Policy: policy.Evaluator{Runner: runner, ProjectRoot: cfg.ProjectRoot},
		Terraform: platformterraform.Client{
			Runner: runner, Dir: filepath.Join(cfg.ProjectRoot, "infrastructure", "terraform", "envs", "dev"),
			DataDir: filepath.Join(home, ".cache", "go-coffeeshop", "terraform", "dev-foundation-"+cfg.AccountID),
			VarFile: cfg.VarFile, Environment: awsEnvironment, Timeout: timeout,
		},
	}
}

func (o *RealOperations) Preflight(ctx context.Context, action Action) error {
	tools := [][]string{{"terraform", "version"}, {"aws", "--version"}}
	if action != ActionStatus {
		tools = append(tools, []string{"conftest", "--version"})
	}
	if action == ActionSetup {
		tools = append(tools,
			[]string{o.executable("ansible"), "--version"},
			[]string{o.executable("ansible-playbook"), "--version"},
			[]string{"kubectl", "version", "--client"},
		)
	}
	for _, tool := range tools {
		if _, err := o.Runner.Run(ctx, command.Request{Name: tool[0], Args: tool[1:], Timeout: 30 * time.Second}); err != nil {
			return fmt.Errorf("required tool %s: %w", tool[0], err)
		}
	}
	account, err := o.AWS.Text(ctx, "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	if err != nil {
		return err
	}
	if account != o.Config.AccountID {
		return fmt.Errorf("active AWS account %s does not match DEV account %s", account, o.Config.AccountID)
	}
	if action == ActionTeardown {
		if err := o.verifyRetainedBackup(ctx); err != nil {
			return err
		}
	}
	if action == ActionSetup {
		if err := validatePrivateFile(o.Config.SSHPrivateKey, "DEV SSH private key"); err != nil {
			return err
		}
		if err := validatePrivateFile(o.Config.AnsibleVaultPasswordFile, "DEV Ansible vault password"); err != nil {
			return err
		}
	}
	return nil
}

func (o *RealOperations) Plan(ctx context.Context, action Action) (Plan, error) {
	if err := o.initRemote(ctx); err != nil {
		return Plan{}, err
	}
	client := o.Terraform
	client.BooleanVariables = map[string]bool{"dev_runtime_enabled": action == ActionSetup}
	artifact, err := client.CreatePlan(ctx, "", "dev-"+string(action), false, nil)
	if err != nil {
		return Plan{}, err
	}
	policyName := "dev-reconcile"
	if action == ActionTeardown {
		policyName = "dev-teardown"
	}
	if err := o.Policy.Terraform(ctx, policyName, artifact.JSONPath); err != nil {
		artifact.Cleanup()
		return Plan{}, err
	}
	human, err := o.Terraform.ShowHuman(ctx, artifact)
	if err != nil {
		artifact.Cleanup()
		return Plan{}, err
	}
	return Plan{Artifact: artifact, Human: human}, nil
}

func (o *RealOperations) Apply(ctx context.Context, plan Plan) error {
	return o.Terraform.Apply(ctx, plan.Artifact)
}

func (o *RealOperations) initRemote(ctx context.Context) error {
	kmsARN, err := o.AWS.Text(ctx, "kms", "describe-key", "--key-id", o.Config.StateKMSKeyID,
		"--query", "KeyMetadata.Arn", "--output", "text")
	if err != nil {
		return fmt.Errorf("resolve DEV state KMS key: %w", err)
	}
	return o.Terraform.InitS3(ctx, platformterraform.S3BackendConfig{
		Bucket: o.Config.StateBucket, Key: o.Config.FoundationStateKey, Region: o.Config.Region,
		KMSKeyARN: kmsARN, RoleARN: o.Config.BackendRoleARN, Encrypt: true, UseLockfile: true,
	})
}

func (o *RealOperations) Configure(ctx context.Context) error {
	devRole, err := o.output(ctx, "github_actions_role_arn")
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Output, "GitHub dev secret DEV_AWS_ROLE_ARN=%s\n", devRole)
	fmt.Fprintf(o.Output, "GitHub dev variable DEV_AWS_REGION=%s\n", o.Config.Region)
	if err := o.waitForInstances(ctx); err != nil {
		return err
	}
	ansibleDir := filepath.Join(o.Config.ProjectRoot, "infrastructure", "ansible")
	environment := o.ansibleEnvironment()
	if err := o.waitForAnsible(ctx, ansibleDir, environment); err != nil {
		return err
	}
	if _, err := o.Runner.Run(ctx, command.Request{
		Name: o.executable("ansible-playbook"),
		Args: []string{"--inventory", "localhost,", "--syntax-check", "playbooks/site.yml"},
		Dir:  ansibleDir, Env: environment, Timeout: 5 * time.Minute, Stream: true,
	}); err != nil {
		return fmt.Errorf("Ansible syntax check: %w", err)
	}
	apiProvider, err := o.output(ctx, "active_api_endpoint_provider")
	if err != nil {
		return err
	}
	apiEndpoint, err := o.output(ctx, "active_api_endpoint")
	if err != nil {
		return err
	}
	registrationEndpoint, err := o.output(ctx, "k3s_registration_endpoint")
	if err != nil {
		return err
	}
	tlsSANs, err := o.Terraform.OutputJSON(ctx, "k3s_tls_sans")
	if err != nil {
		return err
	}
	longhornSize, err := o.output(ctx, "longhorn_data_volume_size")
	if err != nil {
		return err
	}
	backupBucket, err := o.output(ctx, "postgres_backup_bucket_name")
	if err != nil {
		return err
	}
	backupAccessKey, err := o.output(ctx, "postgres_backup_iam_access_key_id")
	if err != nil {
		return err
	}
	backupSecret, err := o.output(ctx, "postgres_backup_iam_secret_access_key")
	if err != nil {
		return err
	}
	environment["POSTGRES_BACKUP_BUCKET_NAME"] = backupBucket
	environment["POSTGRES_BACKUP_IAM_ACCESS_KEY_ID"] = backupAccessKey
	environment["POSTGRES_BACKUP_IAM_SECRET_ACCESS_KEY"] = backupSecret
	redactions := []string{backupAccessKey, backupSecret}
	commonArgs := []string{"--inventory", "inventory/aws_ec2.yml", "--private-key", o.Config.SSHPrivateKey}
	siteArgs := append(append([]string{}, commonArgs...),
		"--extra-vars", "active_api_endpoint_provider="+apiProvider,
		"--extra-vars", "active_api_endpoint="+apiEndpoint,
		"--extra-vars", "k3s_registration_endpoint="+registrationEndpoint,
		"--extra-vars", `{"k3s_tls_sans": `+strings.TrimSpace(tlsSANs)+`}`,
		"--extra-vars", "longhorn_prereqs_data_volume_size="+longhornSize,
		"playbooks/site.yml",
	)
	if _, err := o.Runner.Run(ctx, command.Request{
		Name: o.executable("ansible-playbook"), Args: siteArgs, Dir: ansibleDir, Env: environment,
		Timeout: 60 * time.Minute, Stream: true, Redactions: redactions,
	}); err != nil {
		return fmt.Errorf("configure DEV hosts: %w", err)
	}
	gitOpsArgs := append(append([]string{}, commonArgs...), "playbooks/gitops_cicd.yml")
	if _, err := o.Runner.Run(ctx, command.Request{
		Name: o.executable("ansible-playbook"), Args: gitOpsArgs, Dir: ansibleDir, Env: environment,
		Timeout: 45 * time.Minute, Stream: true, Redactions: redactions,
	}); err != nil {
		return fmt.Errorf("bootstrap DEV GitOps: %w", err)
	}
	return nil
}

func (o *RealOperations) waitForInstances(ctx context.Context) error {
	ids, err := o.AWS.Text(ctx, "ec2", "describe-instances", "--filters",
		"Name=tag:Environment,Values=dev", "Name=tag:ManagedBy,Values=Terraform",
		"Name=instance-state-name,Values=running",
		"--query", "Reservations[].Instances[].InstanceId", "--output", "text")
	if err != nil {
		return err
	}
	instanceIDs := strings.Fields(ids)
	if len(instanceIDs) == 0 {
		return fmt.Errorf("no running DEV instances found after Terraform apply")
	}
	args := append([]string{"ec2", "wait", "instance-status-ok", "--instance-ids"}, instanceIDs...)
	return o.AWS.Run(ctx, args...)
}

func (o *RealOperations) waitForAnsible(ctx context.Context, ansibleDir string, environment map[string]string) error {
	for attempt := 1; attempt <= 30; attempt++ {
		_, err := o.Runner.Run(ctx, command.Request{
			Name: o.executable("ansible"), Args: []string{
				"all", "--module-name", "ansible.builtin.raw",
				"--args", "cloud-init status --wait; status=$?; [ $status -eq 0 ] || [ $status -eq 2 ]",
				"--private-key", o.Config.SSHPrivateKey,
			},
			Dir: ansibleDir, Env: environment, Timeout: 5 * time.Minute,
		})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return fmt.Errorf("DEV instances did not become SSH/cloud-init ready")
}

func (o *RealOperations) Verify(ctx context.Context, action Action) error {
	if action == ActionTeardown {
		return o.verifyTeardown(ctx)
	}
	if action == ActionStatus {
		count, err := o.runningInstanceCount(ctx)
		if err != nil {
			return err
		}
		if count == "0" {
			return o.verifyTeardown(ctx)
		}
	}
	if err := o.initRemote(ctx); err != nil {
		return err
	}
	environment := map[string]string{
		"KUBECONFIG":  o.Config.Kubeconfig,
		"TF_DATA_DIR": o.Terraform.DataDir,
	}
	if o.Config.AWSProfile != "" {
		environment["AWS_PROFILE"] = o.Config.AWSProfile
	}
	result, err := o.Runner.Run(ctx, command.Request{
		Name: "bash", Args: []string{filepath.Join(o.Config.ProjectRoot, "scripts", "verify-dev-runtime.sh")},
		Dir:     o.Config.ProjectRoot,
		Env:     environment,
		Timeout: 30 * time.Minute, Stream: true,
	})
	if err != nil {
		return err
	}
	fmt.Fprint(o.Output, result.Stdout)
	fmt.Fprintln(o.Output, "DEV status passed: Terraform backend and runtime verification are healthy.")
	return nil
}

func (o *RealOperations) runningInstanceCount(ctx context.Context) (string, error) {
	return o.AWS.Text(ctx, "ec2", "describe-instances", "--filters",
		"Name=tag:Environment,Values=dev", "Name=tag:ManagedBy,Values=Terraform",
		"Name=instance-state-name,Values=pending,running,stopping,stopped",
		"--query", "length(Reservations[].Instances[])", "--output", "text")
}

func (o *RealOperations) verifyTeardown(ctx context.Context) error {
	queries := []struct {
		name string
		args []string
	}{
		{"instances", []string{"ec2", "describe-instances", "--filters", "Name=tag:Environment,Values=dev", "Name=tag:ManagedBy,Values=Terraform", "Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down", "--query", "length(Reservations[].Instances[])", "--output", "text"}},
		{"EBS volumes", []string{"ec2", "describe-volumes", "--filters", "Name=tag:Environment,Values=dev", "Name=tag:ManagedBy,Values=Terraform", "--query", "length(Volumes)", "--output", "text"}},
		{"Elastic IPs", []string{"ec2", "describe-addresses", "--filters", "Name=tag:Environment,Values=dev", "Name=tag:ManagedBy,Values=Terraform", "--query", "length(Addresses)", "--output", "text"}},
		{"load balancers", []string{"resourcegroupstaggingapi", "get-resources", "--resource-type-filters", "elasticloadbalancing:loadbalancer", "--tag-filters", "Key=Environment,Values=dev", "Key=ManagedBy,Values=Terraform", "--query", "length(ResourceTagMappingList)", "--output", "text"}},
	}
	for _, query := range queries {
		value, err := o.AWS.Text(ctx, query.args...)
		if err != nil {
			return err
		}
		if value != "0" {
			return fmt.Errorf("DEV teardown inventory still contains %s=%s", query.name, value)
		}
	}
	if err := o.AWS.Run(ctx, "s3api", "head-object", "--bucket", o.Config.StateBucket,
		"--key", o.Config.FoundationStateKey); err != nil {
		return fmt.Errorf("retained DEV Terraform state is missing: %w", err)
	}
	if err := o.verifyRetainedBackup(ctx); err != nil {
		return err
	}
	catalog, err := component.Load(filepath.Join(o.Config.ProjectRoot, "platform", "components.yaml"))
	if err != nil {
		return err
	}
	for _, name := range catalog.Names() {
		entry, findErr := catalog.Find(name)
		if findErr != nil {
			return findErr
		}
		if entry.Kind == "migration" {
			continue
		}
		if err := o.AWS.Run(ctx, "ecr", "describe-repositories", "--repository-names", entry.ImageRepository); err != nil {
			return fmt.Errorf("retained DEV ECR repository %s is missing: %w", entry.ImageRepository, err)
		}
	}
	if err := o.AWS.Run(ctx, "iam", "get-role", "--role-name", o.Config.ClusterName+"-github-actions-role"); err != nil {
		return fmt.Errorf("retained DEV delivery role is missing: %w", err)
	}
	fmt.Fprintln(o.Output, "DEV teardown passed: billable runtime is absent; state, PostgreSQL backup and DEV ECR are retained.")
	return nil
}

func (o *RealOperations) verifyRetainedBackup(ctx context.Context) error {
	bucket := o.Config.ProjectName + "-dev-postgres-backup-" + o.Config.AccountID
	count, err := o.AWS.Text(ctx, "s3api", "list-objects-v2", "--bucket", bucket,
		"--prefix", "coffeeshop-postgres/base/", "--max-keys", "1", "--query", "KeyCount", "--output", "text")
	if err != nil {
		return fmt.Errorf("inspect retained PostgreSQL backup: %w", err)
	}
	if count == "0" || count == "None" || count == "" {
		return fmt.Errorf("no retained PostgreSQL base backup found in s3://%s/coffeeshop-postgres/base/", bucket)
	}
	return nil
}

func (o *RealOperations) ansibleEnvironment() map[string]string {
	ansibleDir := filepath.Join(o.Config.ProjectRoot, "infrastructure", "ansible")
	environment := map[string]string{
		"ANSIBLE_CONFIG":              filepath.Join(ansibleDir, "ansible.cfg"),
		"ANSIBLE_INVENTORY":           filepath.Join(ansibleDir, "inventory", "aws_ec2.yml"),
		"ANSIBLE_HOST_KEY_CHECKING":   "True",
		"ANSIBLE_REMOTE_USER":         "ubuntu",
		"ANSIBLE_FORKS":               "10",
		"ANSIBLE_ROLES_PATH":          filepath.Join(ansibleDir, "roles"),
		"ANSIBLE_INVENTORY_ENABLED":   "host_list,script,auto,yaml,ini,toml,amazon.aws.aws_ec2",
		"ANSIBLE_SSH_PIPELINING":      "True",
		"ANSIBLE_SSH_ARGS":            "-o StrictHostKeyChecking=accept-new",
		"ANSIBLE_PRIVATE_KEY_FILE":    o.Config.SSHPrivateKey,
		"ANSIBLE_VAULT_PASSWORD_FILE": o.Config.AnsibleVaultPasswordFile,
		"AWS_REGION":                  o.Config.Region,
		"AWS_DEFAULT_REGION":          o.Config.Region,
	}
	if o.Config.AWSProfile != "" {
		environment["AWS_PROFILE"] = o.Config.AWSProfile
	}
	return environment
}

func (o *RealOperations) executable(name string) string {
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".venvs", "go-coffeeshop-platform", "bin", name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

func (o *RealOperations) output(ctx context.Context, name string) (string, error) {
	value, err := o.Terraform.Output(ctx, name)
	if err != nil {
		return "", fmt.Errorf("read DEV Terraform output %s: %w", name, err)
	}
	return strings.TrimSpace(value), nil
}

func validatePrivateFile(path, description string) error {
	if path == "" {
		return fmt.Errorf("%s path is required", description)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", description, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a regular file inaccessible by group or others", description)
	}
	if strings.HasPrefix(path, "/mnt/") {
		return fmt.Errorf("%s must be stored on the WSL filesystem, not under /mnt", description)
	}
	return nil
}
