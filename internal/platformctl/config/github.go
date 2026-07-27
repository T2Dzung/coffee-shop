package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var githubSecretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type GitHub struct {
	TerraformDir         string
	VarFile              string
	Region               string
	AWSProfile           string
	AccountID            string
	StateBucket          string
	StateKey             string
	StateKMSKeyID        string
	BackendRoleARN       string
	Owner                string
	Repository           string
	GovernanceToken      string
	RepositorySecretData map[string]string
}

func (l Loader) LoadGitHub(projectRoot string) (GitHub, error) {
	if l.LookupEnv == nil {
		l.LookupEnv = os.LookupEnv
	}
	prod, err := l.LoadProd(projectRoot, "")
	if err != nil {
		return GitHub{}, fmt.Errorf("load PROD backend contract for GitHub governance: %w", err)
	}
	operator, err := l.LoadOperator()
	if err != nil {
		return GitHub{}, err
	}
	local := operator.File.GitHub
	cfg := GitHub{
		TerraformDir:         filepath.Join(projectRoot, "infrastructure", "terraform", "github"),
		VarFile:              local.TerraformVarFile,
		Region:               prod.Region,
		AWSProfile:           prod.AWSProfile,
		AccountID:            prod.AccountID,
		StateBucket:          prod.StateBucket,
		StateKey:             "prod/github-governance.tfstate",
		StateKMSKeyID:        envString(l.LookupEnv, "PROD_STATE_KMS_KEY_ID", "alias/"+prod.ProjectName+"-state-key"),
		BackendRoleARN:       prod.BackendRoleARN,
		RepositorySecretData: map[string]string{},
	}
	if cfg.VarFile == "" {
		cfg.VarFile = filepath.Join(cfg.TerraformDir, "terraform.tfvars")
	}
	attrs, err := parseAttributes(cfg.VarFile)
	if err != nil {
		return GitHub{}, err
	}
	cfg.Owner = stringValue(attrs, "github_owner", "")
	cfg.Repository = stringValue(attrs, "repository_name", "")
	cfg.GovernanceToken = strings.TrimSpace(envString(l.LookupEnv, "GITHUB_TOKEN", ""))
	if cfg.GovernanceToken == "" {
		cfg.GovernanceToken, err = readSecretFile(local.GovernanceTokenFile, "GitHub governance token")
		if err != nil {
			return GitHub{}, err
		}
	}
	for name, path := range local.RepositorySecretFiles {
		if !githubSecretNamePattern.MatchString(name) {
			return GitHub{}, fmt.Errorf("invalid GitHub secret name %q", name)
		}
		value, readErr := readSecretFile(path, "GitHub secret "+name)
		if readErr != nil {
			return GitHub{}, readErr
		}
		cfg.RepositorySecretData[name] = value
	}
	if cfg.Owner == "" || cfg.Repository == "" {
		return GitHub{}, fmt.Errorf("github_owner and repository_name are required in %s", cfg.VarFile)
	}
	if fullName := cfg.Owner + "/" + cfg.Repository; fullName != prod.GitHubRepository {
		return GitHub{}, fmt.Errorf(
			"GitHub governance target %s does not match PROD github_repository %s",
			fullName, prod.GitHubRepository,
		)
	}
	if cfg.GovernanceToken == "" {
		return GitHub{}, fmt.Errorf("GitHub governance token is required through GITHUB_TOKEN or github.governanceTokenFile")
	}
	return cfg, nil
}
