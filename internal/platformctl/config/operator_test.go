package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOperatorRejectsUnknownField(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "operator.yaml")
	require.NoError(t, os.WriteFile(path, []byte("schemaVersion: 1\nunknown: true\n"), 0o600))
	_, err := (Loader{OperatorConfigPath: path}).LoadOperator()
	require.ErrorContains(t, err, "field unknown not found")
}

func TestLoadCIUsesOperatorConfigThenEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	tfvars := filepath.Join(dir, "ci.tfvars")
	require.NoError(t, os.WriteFile(tfvars, []byte(`
expected_aws_account_id = "123456789012"
aws_region = "ap-southeast-1"
github_repository = "owner/repository"
`), 0o600))
	key := filepath.Join(dir, "ci.pem")
	token := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(key, []byte("private"), 0o600))
	require.NoError(t, os.WriteFile(token, []byte("secret-token\n"), 0o600))
	operator := filepath.Join(dir, "operator.yaml")
	require.NoError(t, os.WriteFile(operator, []byte(`schemaVersion: 1
environments:
  ci:
    terraformVarFile: ci.tfvars
    awsProfile: from-file
    sshPrivateKeyFile: ci.pem
    githubAuth:
      mode: pat
      personalTokenFile: token
`), 0o600))
	env := map[string]string{"CI_AWS_PROFILE": "from-env"}
	cfg, err := (Loader{
		OperatorConfigPath: operator,
		LookupEnv:          func(key string) (string, bool) { value, ok := env[key]; return value, ok },
		HomeDir:            func() (string, error) { return dir, nil },
	}).LoadCI("/repo", "")
	require.NoError(t, err)
	require.Equal(t, "from-env", cfg.AWSProfile)
	require.Equal(t, key, cfg.SSHPrivateKey)
	require.Equal(t, "secret-token", cfg.GitHubToken)
}

func TestLoadOperatorRejectsLooseSecretPermission(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o644))
	_, err := readSecretFile(path, "token")
	require.ErrorContains(t, err, "group or others")
}

func TestLoadGitHubResolvesSecretFilesWithoutPuttingValuesInYAML(t *testing.T) {
	dir := t.TempDir()
	tfvars := filepath.Join(dir, "github.tfvars")
	prodTFVars := filepath.Join(dir, "prod.tfvars")
	token := filepath.Join(dir, "governance-token")
	telegram := filepath.Join(dir, "telegram-token")
	require.NoError(t, os.WriteFile(tfvars, []byte(`
github_owner = "owner"
repository_name = "repo"
`), 0o600))
	require.NoError(t, os.WriteFile(prodTFVars, []byte(`
project_name = "coffeeshop"
environment = "prod"
aws_region = "ap-southeast-1"
expected_aws_account_id = "123456789012"
github_repository = "owner/repo"
cluster_endpoint_public_access_cidrs = ["203.0.113.10/32"]
node_instance_types = ["t3.medium"]
node_desired_size = 2
node_disk_size = 20
`), 0o600))
	require.NoError(t, os.WriteFile(token, []byte("governance"), 0o600))
	require.NoError(t, os.WriteFile(telegram, []byte("telegram"), 0o600))
	operator := filepath.Join(dir, "operator.yaml")
	require.NoError(t, os.WriteFile(operator, []byte(`schemaVersion: 1
github:
  terraformVarFile: github.tfvars
  governanceTokenFile: governance-token
  repositorySecretFiles:
    TELEGRAM_TOKEN: telegram-token
environments:
  prod:
    terraformVarFile: prod.tfvars
    awsProfile: coffeeshop-prod
`), 0o600))
	cfg, err := (Loader{OperatorConfigPath: operator}).LoadGitHub("/repo")
	require.NoError(t, err)
	require.Equal(t, "owner", cfg.Owner)
	require.Equal(t, "repo", cfg.Repository)
	require.Equal(t, "governance", cfg.GovernanceToken)
	require.Equal(t, "telegram", cfg.RepositorySecretData["TELEGRAM_TOKEN"])
	require.Equal(t, "coffeeshop-prod", cfg.AWSProfile)
	require.Equal(t, "coffeeshop-terraform-state-123456789012", cfg.StateBucket)
	require.Equal(t, "prod/github-governance.tfstate", cfg.StateKey)
	require.Equal(t, "alias/coffeeshop-state-key", cfg.StateKMSKeyID)
}

func TestLoadGitHubRejectsRepositoryDifferentFromProd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "github.tfvars"), []byte(`
github_owner = "other"
repository_name = "repo"
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prod.tfvars"), []byte(`
project_name = "coffeeshop"
environment = "prod"
aws_region = "ap-southeast-1"
expected_aws_account_id = "123456789012"
github_repository = "owner/repo"
cluster_endpoint_public_access_cidrs = ["203.0.113.10/32"]
node_instance_types = ["t3.medium"]
node_desired_size = 2
node_disk_size = 20
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "token"), []byte("governance"), 0o600))
	operator := filepath.Join(dir, "operator.yaml")
	require.NoError(t, os.WriteFile(operator, []byte(`schemaVersion: 1
github:
  terraformVarFile: github.tfvars
  governanceTokenFile: token
environments:
  prod:
    terraformVarFile: prod.tfvars
`), 0o600))
	_, err := (Loader{OperatorConfigPath: operator}).LoadGitHub("/repo")
	require.ErrorContains(t, err, "does not match PROD github_repository")
}
