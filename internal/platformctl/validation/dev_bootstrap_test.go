package validation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type argoApplicationSource struct {
	Spec struct {
		Source struct {
			Path string `yaml:"path"`
		} `yaml:"source"`
	} `yaml:"spec"`
}

type ansiblePlay struct {
	Roles []struct {
		Role string `yaml:"role"`
	} `yaml:"roles"`
}

func TestDevGitOpsBootstrapSeparatesPlatformAndRuntimeLayers(t *testing.T) {
	t.Parallel()
	root := projectRootFromTest(t)
	platformRoot := readArgoSourcePath(t, filepath.Join(
		root, "infrastructure/k8s/environments/dev/bootstrap/root-app.yaml",
	))
	runtimeRoot := readArgoSourcePath(t, filepath.Join(
		root, "infrastructure/k8s/environments/dev/bootstrap/runtime-root-app.yaml",
	))

	require.Equal(t, "infrastructure/k8s/environments/dev/gitops/applications", platformRoot)
	require.Equal(t, "infrastructure/k8s/environments/dev/gitops/runtime-applications", runtimeRoot)
	for _, name := range []string{"coffeeshop.yaml", "platform-ownership-guard.yaml"} {
		_, err := os.Stat(filepath.Join(root, platformRoot, name))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.FileExists(t, filepath.Join(root, runtimeRoot, name))
	}
}

func TestDevGitOpsBootstrapOpensRuntimeAfterDataApplications(t *testing.T) {
	t.Parallel()
	root := projectRootFromTest(t)
	content, err := os.ReadFile(filepath.Join(
		root, "infrastructure/ansible/playbooks/gitops_cicd.yml",
	))
	require.NoError(t, err)
	var plays []ansiblePlay
	require.NoError(t, yaml.Unmarshal(content, &plays))
	require.Len(t, plays, 1)

	var roles []string
	for _, item := range plays[0].Roles {
		roles = append(roles, item.Role)
	}
	postgres := indexOf(roles, "coffeeshop_postgres_app")
	rabbit := indexOf(roles, "coffeeshop_rabbitmq_app")
	runtime := indexOf(roles, "coffeeshop_runtime_apps")
	require.NotEqual(t, -1, postgres)
	require.NotEqual(t, -1, rabbit)
	require.NotEqual(t, -1, runtime)
	require.Less(t, postgres, runtime)
	require.Less(t, rabbit, runtime)
}

func readArgoSourcePath(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var application argoApplicationSource
	require.NoError(t, yaml.Unmarshal(content, &application))
	return application.Spec.Source.Path
}

func projectRootFromTest(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(workingDirectory, "..", "..", ".."))
}

func indexOf(values []string, expected string) int {
	for index, value := range values {
		if value == expected {
			return index
		}
	}
	return -1
}
