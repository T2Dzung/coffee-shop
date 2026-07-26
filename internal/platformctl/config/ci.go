package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type CI struct {
	ProjectRoot             string
	VarFile                 string
	ProjectName             string
	Environment             string
	Region                  string
	AWSProfile              string
	AccountID               string
	GitHubRepository        string
	InstanceType            string
	RootVolumeGiB           int
	KeyName                 string
	OperatorSSHCIDRs        []string
	StateBucket             string
	StateKey                string
	StateKMSKeyID           string
	BackendRoleARN          string
	SSHPrivateKey           string
	Kubeconfig              string
	GitHubAuthMode          string
	GitHubAppID             string
	GitHubAppInstallationID string
	GitHubAppPrivateKey     string
	GitHubToken             string
	MaxRunners              int
	AutoApprove             bool
}

func (l Loader) LoadCI(projectRoot, varFile string) (CI, error) {
	if l.LookupEnv == nil {
		l.LookupEnv = os.LookupEnv
	}
	if l.HomeDir == nil {
		l.HomeDir = os.UserHomeDir
	}
	operator, err := l.LoadOperator()
	if err != nil {
		return CI{}, err
	}
	local := operator.File.Environments.CI
	if varFile == "" {
		varFile = envString(l.LookupEnv, "CI_VAR_FILE", local.VarFile)
		if varFile == "" {
			varFile = filepath.Join(projectRoot, "infrastructure", "terraform", "envs", "ci", "terraform.tfvars")
		}
	}
	attrs, err := parseAttributes(varFile)
	if err != nil {
		return CI{}, err
	}
	home, err := l.HomeDir()
	if err != nil {
		return CI{}, fmt.Errorf("resolve home directory: %w", err)
	}
	cfg := CI{
		ProjectRoot:             projectRoot,
		VarFile:                 varFile,
		ProjectName:             stringValue(attrs, "project_name", "coffeeshop"),
		Environment:             stringValue(attrs, "environment", "ci"),
		Region:                  stringValue(attrs, "aws_region", ""),
		AccountID:               stringValue(attrs, "expected_aws_account_id", ""),
		GitHubRepository:        stringValue(attrs, "github_repository", ""),
		InstanceType:            stringValue(attrs, "instance_type", "t3.large"),
		RootVolumeGiB:           intValue(attrs, "root_volume_size", 40),
		KeyName:                 stringValue(attrs, "key_name", ""),
		OperatorSSHCIDRs:        stringsValue(attrs, "operator_ssh_cidrs", nil),
		StateKey:                "ci/foundation.tfstate",
		GitHubAuthMode:          "github_app",
		MaxRunners:              2,
		SSHPrivateKey:           envString(l.LookupEnv, "CI_SSH_PRIVATE_KEY", ""),
		StateKMSKeyID:           envString(l.LookupEnv, "CI_STATE_KMS_KEY_ID", "alias/coffeeshop-state-key"),
		BackendRoleARN:          envString(l.LookupEnv, "CI_BACKEND_ROLE_ARN", ""),
		Kubeconfig:              filepath.Join(home, ".kube", "coffeeshop-ci.yaml"),
		GitHubAppID:             envString(l.LookupEnv, "ARC_GITHUB_APP_ID", ""),
		GitHubAppInstallationID: envString(l.LookupEnv, "ARC_GITHUB_APP_INSTALLATION_ID", ""),
		GitHubAppPrivateKey:     envString(l.LookupEnv, "ARC_GITHUB_APP_PRIVATE_KEY", ""),
		GitHubToken:             envString(l.LookupEnv, "ARC_GITHUB_TOKEN", ""),
	}
	if local.AWSProfile != "" {
		cfg.AWSProfile = local.AWSProfile
	}
	if local.SSHPrivateKeyFile != "" {
		cfg.SSHPrivateKey = local.SSHPrivateKeyFile
	}
	if local.Kubeconfig != "" {
		cfg.Kubeconfig = local.Kubeconfig
	}
	if local.GitHubAuth.Mode != "" {
		cfg.GitHubAuthMode = local.GitHubAuth.Mode
	}
	if local.GitHubAuth.AppID != "" {
		cfg.GitHubAppID = local.GitHubAuth.AppID
	}
	if local.GitHubAuth.InstallationID != "" {
		cfg.GitHubAppInstallationID = local.GitHubAuth.InstallationID
	}
	if local.MaxRunners > 0 {
		cfg.MaxRunners = local.MaxRunners
	}
	if cfg.GitHubAppPrivateKey == "" && local.GitHubAuth.AppPrivateKeyFile != "" {
		cfg.GitHubAppPrivateKey, err = readSecretFile(local.GitHubAuth.AppPrivateKeyFile, "GitHub App private key")
		if err != nil {
			return CI{}, err
		}
	}
	if cfg.GitHubToken == "" && local.GitHubAuth.PersonalTokenFile != "" {
		cfg.GitHubToken, err = readSecretFile(local.GitHubAuth.PersonalTokenFile, "GitHub token")
		if err != nil {
			return CI{}, err
		}
	}
	cfg.AWSProfile = envString(l.LookupEnv, "CI_AWS_PROFILE", cfg.AWSProfile)
	cfg.SSHPrivateKey = envString(l.LookupEnv, "CI_SSH_PRIVATE_KEY", cfg.SSHPrivateKey)
	cfg.StateBucket = envString(l.LookupEnv, "CI_STATE_BUCKET_NAME",
		cfg.ProjectName+"-terraform-state-"+cfg.AccountID)
	cfg.BackendRoleARN = envString(l.LookupEnv, "CI_BACKEND_ROLE_ARN",
		"arn:aws:iam::"+cfg.AccountID+":role/"+cfg.ProjectName+"-ci-terraform-backend-role")
	cfg.StateKey = envString(l.LookupEnv, "CI_STATE_KEY", cfg.StateKey)
	cfg.StateKMSKeyID = envString(l.LookupEnv, "CI_STATE_KMS_KEY_ID",
		"alias/"+cfg.ProjectName+"-state-key")
	cfg.Kubeconfig = envString(l.LookupEnv, "CI_KUBECONFIG", cfg.Kubeconfig)
	cfg.GitHubAuthMode = envString(l.LookupEnv, "ARC_GITHUB_AUTH_MODE", cfg.GitHubAuthMode)
	cfg.GitHubAppID = envString(l.LookupEnv, "ARC_GITHUB_APP_ID", cfg.GitHubAppID)
	cfg.GitHubAppInstallationID = envString(l.LookupEnv, "ARC_GITHUB_APP_INSTALLATION_ID", cfg.GitHubAppInstallationID)
	cfg.GitHubAppPrivateKey = envString(l.LookupEnv, "ARC_GITHUB_APP_PRIVATE_KEY", cfg.GitHubAppPrivateKey)
	cfg.GitHubToken = envString(l.LookupEnv, "ARC_GITHUB_TOKEN", cfg.GitHubToken)
	cfg.MaxRunners = envInt(l.LookupEnv, "ARC_MAX_RUNNERS", cfg.MaxRunners)
	cfg.AutoApprove = envString(l.LookupEnv, "CI_AUTO_APPROVE", "false") == "true"
	if err := cfg.Validate(); err != nil {
		return CI{}, err
	}
	return cfg, nil
}

func (c CI) Validate() error {
	var problems []string
	if c.Environment != "ci" {
		problems = append(problems, "CI environment must be ci")
	}
	if !accountPattern.MatchString(c.AccountID) {
		problems = append(problems, "expected AWS account ID must contain 12 digits")
	}
	if c.Region == "" {
		problems = append(problems, "AWS Region is required")
	}
	if !repoPattern.MatchString(c.GitHubRepository) {
		problems = append(problems, "GitHub repository must use owner/repository form")
	}
	if c.StateKey != "ci/foundation.tfstate" {
		problems = append(problems, "CI state key must be exactly ci/foundation.tfstate")
	}
	if c.InstanceType == "" || c.RootVolumeGiB < 20 || c.MaxRunners < 1 {
		problems = append(problems, "instance type, root volume and max runners must be within the reviewed range")
	}
	if c.GitHubAuthMode != "github_app" && c.GitHubAuthMode != "pat" {
		problems = append(problems, "ARC_GITHUB_AUTH_MODE must be github_app or pat")
	}
	return errors.Join(stringsToErrors(problems)...)
}
