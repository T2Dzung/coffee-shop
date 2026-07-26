package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Dev struct {
	ProjectRoot              string
	VarFile                  string
	ProjectName              string
	Environment              string
	Region                   string
	AWSProfile               string
	AccountID                string
	GitHubRepository         string
	ClusterName              string
	NodeCount                int
	InstanceType             string
	RootVolumeGiB            int
	LonghornVolumeGiB        int
	StateBucket              string
	StateKMSKeyID            string
	BackendRoleARN           string
	BootstrapStateKey        string
	FoundationStateKey       string
	SSHPrivateKey            string
	AnsibleVaultPasswordFile string
	Kubeconfig               string
	AutoApprove              bool
}

func (l Loader) LoadDev(projectRoot, varFile string) (Dev, error) {
	if l.LookupEnv == nil {
		l.LookupEnv = os.LookupEnv
	}
	if l.HomeDir == nil {
		l.HomeDir = os.UserHomeDir
	}
	operator, err := l.LoadOperator()
	if err != nil {
		return Dev{}, err
	}
	local := operator.File.Environments.Dev
	if varFile == "" {
		varFile = envString(l.LookupEnv, "DEV_VAR_FILE", local.VarFile)
		if varFile == "" {
			varFile = filepath.Join(projectRoot, "infrastructure", "terraform", "envs", "dev", "terraform.tfvars")
		}
	}
	attrs, err := parseAttributes(varFile)
	if err != nil {
		return Dev{}, err
	}
	home, err := l.HomeDir()
	if err != nil {
		return Dev{}, fmt.Errorf("resolve home directory: %w", err)
	}
	cfg := Dev{
		ProjectRoot:              projectRoot,
		VarFile:                  varFile,
		ProjectName:              stringValue(attrs, "project_name", "coffeeshop"),
		Environment:              stringValue(attrs, "environment", "dev"),
		Region:                   stringValue(attrs, "aws_region", ""),
		AccountID:                stringValue(attrs, "expected_aws_account_id", ""),
		GitHubRepository:         stringValue(attrs, "github_repository", ""),
		ClusterName:              stringValue(attrs, "cluster_name", "coffeeshop-dev"),
		NodeCount:                intValue(attrs, "node_count", 3),
		InstanceType:             stringValue(attrs, "k3s_instance_type", "t3.large"),
		RootVolumeGiB:            intValue(attrs, "k3s_root_volume_size", 30),
		LonghornVolumeGiB:        intValue(attrs, "longhorn_data_volume_size", 50),
		BootstrapStateKey:        "dev/bootstrap.tfstate",
		FoundationStateKey:       "dev/terraform.tfstate",
		StateKMSKeyID:            "alias/coffeeshop-state-key",
		Kubeconfig:               filepath.Join(home, ".kube", "coffeeshop-dev.yaml"),
		AnsibleVaultPasswordFile: filepath.Join(home, ".vault-pass"),
	}
	if local.AWSProfile != "" {
		cfg.AWSProfile = local.AWSProfile
	}
	if local.SSHPrivateKeyFile != "" {
		cfg.SSHPrivateKey = local.SSHPrivateKeyFile
	}
	if local.AnsibleVaultPasswordFile != "" {
		cfg.AnsibleVaultPasswordFile = local.AnsibleVaultPasswordFile
	}
	if local.Kubeconfig != "" {
		cfg.Kubeconfig = local.Kubeconfig
	}
	cfg.AWSProfile = envString(l.LookupEnv, "DEV_AWS_PROFILE", cfg.AWSProfile)
	cfg.AccountID = envString(l.LookupEnv, "DEV_EXPECTED_AWS_ACCOUNT_ID", cfg.AccountID)
	cfg.SSHPrivateKey = envString(l.LookupEnv, "ANSIBLE_PRIVATE_KEY_FILE", cfg.SSHPrivateKey)
	cfg.AnsibleVaultPasswordFile = envString(l.LookupEnv, "ANSIBLE_VAULT_PASSWORD_FILE", cfg.AnsibleVaultPasswordFile)
	cfg.Kubeconfig = envString(l.LookupEnv, "DEV_KUBECONFIG", cfg.Kubeconfig)
	cfg.StateBucket = envString(l.LookupEnv, "DEV_STATE_BUCKET_NAME", cfg.ProjectName+"-terraform-state-"+cfg.AccountID)
	cfg.StateKMSKeyID = envString(l.LookupEnv, "DEV_STATE_KMS_KEY_ID", "alias/"+cfg.ProjectName+"-state-key")
	cfg.BackendRoleARN = envString(l.LookupEnv, "DEV_BACKEND_ROLE_ARN",
		"arn:aws:iam::"+cfg.AccountID+":role/"+cfg.ProjectName+"-terraform-backend-role")
	cfg.BootstrapStateKey = envString(l.LookupEnv, "DEV_BOOTSTRAP_STATE_KEY", cfg.BootstrapStateKey)
	cfg.FoundationStateKey = envString(l.LookupEnv, "DEV_FOUNDATION_STATE_KEY", cfg.FoundationStateKey)
	cfg.AutoApprove = envString(l.LookupEnv, "DEV_AUTO_APPROVE", "false") == "true"
	if err := cfg.Validate(); err != nil {
		return Dev{}, err
	}
	return cfg, nil
}

func (c Dev) Validate() error {
	var problems []string
	if c.Environment != "dev" {
		problems = append(problems, "DEV environment must be dev")
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
	if c.FoundationStateKey != "dev/terraform.tfstate" || c.BootstrapStateKey != "dev/bootstrap.tfstate" {
		problems = append(problems, "DEV state keys must remain inside the dev prefix")
	}
	if c.NodeCount < 3 || c.NodeCount%2 == 0 {
		problems = append(problems, "DEV K3s node count must be odd and at least 3")
	}
	if c.InstanceType == "" || c.RootVolumeGiB < 20 || c.LonghornVolumeGiB < 10 {
		problems = append(problems, "DEV instance and volume settings are outside the reviewed range")
	}
	return errors.Join(stringsToErrors(problems)...)
}
