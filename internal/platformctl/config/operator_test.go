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
