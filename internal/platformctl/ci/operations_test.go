package ci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
)

func TestValidateCandidateRepositoriesUsesCatalogAndAWSBoundary(t *testing.T) {
	t.Parallel()
	root := candidateCatalogProject(t)
	expected := []string{
		"ecr", "describe-repositories", "--repository-names",
		"coffeeshop-candidate-platform-ownership-guard",
		"coffeeshop-candidate-go-coffeeshop-web",
	}
	runner := &command.FakeRunner{Expectations: []command.Expectation{{Name: "aws", Args: expected}}}
	operations := &RealOperations{
		Config: config.CI{ProjectRoot: root},
		AWS:    platformaws.Client{Runner: runner},
	}
	require.NoError(t, operations.validateCandidateRepositories(context.Background()))
	require.NoError(t, runner.Verify())
}

func TestValidateCandidateRepositoriesPreservesAWSFailure(t *testing.T) {
	t.Parallel()
	root := candidateCatalogProject(t)
	expected := []string{
		"ecr", "describe-repositories", "--repository-names",
		"coffeeshop-candidate-platform-ownership-guard",
		"coffeeshop-candidate-go-coffeeshop-web",
	}
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: expected, Err: errors.New("AccessDeniedException"),
	}}}
	operations := &RealOperations{
		Config: config.CI{ProjectRoot: root},
		AWS:    platformaws.Client{Runner: runner},
	}
	err := operations.validateCandidateRepositories(context.Background())
	require.ErrorContains(t, err, "verify AWS identity/API availability")
	require.ErrorContains(t, err, "AccessDeniedException")
}

func candidateCatalogProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	platform := filepath.Join(root, "platform")
	require.NoError(t, os.MkdirAll(platform, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(platform, "components.yaml"), []byte(`
schemaVersion: 1
components:
  - name: web
    kind: service
    build: go
    context: .
    dockerfile: docker/Dockerfile-web
    imageRepository: go-coffeeshop-web
    kustomizeImage: web
    devOverlay: dev
    prodOverlay: prod
    automatic: true
    paths: [cmd/web/**]
  - name: guard
    kind: operator
    build: operator
    context: guard
    dockerfile: guard/Dockerfile
    imageRepository: platform-ownership-guard
    kustomizeImage: controller
    devOverlay: guard/dev
    prodOverlay: guard/prod
    automatic: true
    paths: [guard/**]
`), 0o600))
	return root
}
