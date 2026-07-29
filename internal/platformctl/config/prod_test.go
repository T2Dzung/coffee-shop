package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadProdPrecedenceAndDefaults(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	require.NoError(t, os.WriteFile(path, []byte(`
project_name = "coffee"
environment = "prod"
aws_region = "ap-southeast-1"
expected_aws_account_id = "123456789012"
github_repository = "owner/repo"
cluster_endpoint_public_access_cidrs = ["203.0.113.10/32"]
node_instance_types = ["t3.medium"]
node_desired_size = 2
slo_runtime_enabled = true
synthetics_runtime_version = "syn-nodejs-5.2"
`), 0o600))
	env := map[string]string{"PROD_EXPECTED_AWS_REGION": "us-east-1"}
	loader := Loader{
		LookupEnv: func(key string) (string, bool) { value, ok := env[key]; return value, ok },
		HomeDir:   func() (string, error) { return "/home/test", nil },
	}

	cfg, err := loader.LoadProd("/repo", path)
	require.NoError(t, err)
	require.Equal(t, "us-east-1", cfg.Region)
	require.Equal(t, 2, cfg.NodeDesiredSize)
	require.Equal(t, 20, cfg.NodeDiskGiB)
	require.True(t, cfg.SLOEnabled)
	require.Equal(t, "syn-nodejs-5.2", cfg.SyntheticsRuntime)
	require.Equal(t, "/home/test/.kube/coffee-prod.yaml", cfg.Kubeconfig)
}

func TestLoadProdRejectsUnsafeCIDR(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	require.NoError(t, os.WriteFile(path, []byte(`
aws_region = "ap-southeast-1"
expected_aws_account_id = "123456789012"
github_repository = "owner/repo"
cluster_endpoint_public_access_cidrs = ["0.0.0.0/0"]
`), 0o600))
	loader := Loader{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return "/home/test", nil },
	}

	_, err := loader.LoadProd("/repo", path)
	require.ErrorContains(t, err, "cannot be 0.0.0.0/0")
}
