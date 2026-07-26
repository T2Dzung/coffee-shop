package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCIUsesIsolatedStateContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfvars")
	require.NoError(t, os.WriteFile(path, []byte(`
expected_aws_account_id = "123456789012"
aws_region = "ap-southeast-1"
github_repository = "owner/repository"
instance_type = "t3.large"
root_volume_size = 40
`), 0o600))
	cfg, err := (Loader{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return dir, nil },
	}).LoadCI(dir, path)
	require.NoError(t, err)
	require.Equal(t, "ci", cfg.Environment)
	require.Equal(t, "ci/foundation.tfstate", cfg.StateKey)
	require.Equal(t, "coffeeshop-terraform-state-123456789012", cfg.StateBucket)
}

func TestCIRejectsCrossEnvironmentStateKey(t *testing.T) {
	cfg := CI{
		Environment: "ci", AccountID: "123456789012", Region: "ap-southeast-1",
		GitHubRepository: "owner/repository", StateKey: "prod/foundation.tfstate",
		InstanceType: "t3.large", RootVolumeGiB: 40, GitHubAuthMode: "github_app",
	}
	require.ErrorContains(t, cfg.Validate(), "ci/foundation.tfstate")
}

func TestCIRejectsWrongAccountShape(t *testing.T) {
	cfg := CI{
		Environment: "ci", AccountID: "123", Region: "ap-southeast-1",
		GitHubRepository: "owner/repository", StateKey: "ci/foundation.tfstate",
		InstanceType: "t3.large", RootVolumeGiB: 40, GitHubAuthMode: "github_app",
	}
	require.ErrorContains(t, cfg.Validate(), "12 digits")
}
