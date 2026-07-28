package component

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadCatalogRejectsDuplicate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "components.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
schemaVersion: 1
components:
  - &component
    name: web
    kind: service
    build: go
    moduleRoot: .
    package: ./cmd/web
    binary: bin/web
    dockerfile: docker/Dockerfile-web
    imageRepository: web
    kustomizeImage: web
    context: .
    devOverlay: dev
    prodOverlay: prod
    paths: [cmd/web/**]
  - *component
`), 0o600))
	_, err := Load(path)
	require.ErrorContains(t, err, "duplicated")
}

func TestSelectUsesCatalogPathsAndExcludesMigration(t *testing.T) {
	t.Parallel()
	catalog := Catalog{
		SchemaVersion: 1,
		SharedPaths:   []string{"go.mod"},
		Components: []Component{
			{Name: "web", Kind: "service", Automatic: true, Paths: []string{"cmd/web/**"}},
			{Name: "guard", Kind: "operator", Automatic: true, Paths: []string{"guard/**"}},
			{Name: "migrate", Kind: "migration", Paths: []string{"db/**"}},
		},
	}
	require.Equal(t, []string{"web"}, catalog.Select([]string{"cmd/web/main.go"}))
	require.Equal(t, []string{"web"}, catalog.Select([]string{"go.mod"}))
	require.Empty(t, catalog.Select([]string{"db/migrate.go"}))
}

func TestRepositoryCatalogDoesNotRebuildGuardForEnvironmentDelivery(t *testing.T) {
	t.Parallel()
	catalogPath := filepath.Join("..", "..", "..", "platform", "components.yaml")
	catalog, err := Load(catalogPath)
	require.NoError(t, err)

	for _, desiredState := range []string{
		"platform-ownership-guard/config/dev/kustomization.yaml",
		"platform-ownership-guard/config/prod/kustomization.yaml",
		"go.mod",
	} {
		require.NotContains(t, catalog.Select([]string{desiredState}), "platform-ownership-guard")
	}

	for _, imageInput := range []string{
		"platform-ownership-guard/internal/controller/ownershipaudit_controller.go",
		"platform-ownership-guard/cmd/main.go",
		"platform-ownership-guard/go.sum",
		"platform-ownership-guard/Dockerfile.release",
	} {
		require.Contains(t, catalog.Select([]string{imageInput}), "platform-ownership-guard")
	}
}

func TestResolveRequiresExplicitMigrationIntent(t *testing.T) {
	t.Parallel()
	catalog := Catalog{Components: []Component{
		{Name: "web", Kind: "service"},
		{Name: "migrate", Kind: "migration"},
	}}
	_, err := catalog.Resolve([]string{"migrate"}, false)
	require.ErrorContains(t, err, "--allow-migration")
	resolved, err := catalog.Resolve([]string{"web", "migrate", "web"}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"migrate", "web"}, resolved)
}

func TestCandidateRepositoryNamesUseResolvedCatalogMetadata(t *testing.T) {
	t.Parallel()
	catalog := Catalog{Components: []Component{
		{Name: "web", ImageRepository: "go-coffeeshop-web"},
		{Name: "guard", ImageRepository: "platform-ownership-guard"},
	}}
	repositories, err := catalog.CandidateRepositoryNames([]string{"web", "guard", "web"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"coffeeshop-candidate-platform-ownership-guard",
		"coffeeshop-candidate-go-coffeeshop-web",
	}, repositories)
}

func TestValidateChangedFilesUsesKindSpecificBoundaries(t *testing.T) {
	t.Parallel()
	catalog := Catalog{
		SharedPaths: []string{"go.mod", "internal/pkg/**"},
		Components: []Component{
			{Name: "web", Kind: "service", Paths: []string{"cmd/web/**", "internal/web/**"}},
			{Name: "guard", Kind: "operator", Paths: []string{"guard/cmd/**", "guard/internal/**"}},
		},
	}

	require.NoError(t, catalog.ValidateChangedFiles("web", []string{"cmd/web/main.go", "internal/pkg/log.go"}))
	require.NoError(t, catalog.ValidateChangedFiles("guard", []string{"guard/internal/controller/reconcile.go"}))
	require.ErrorContains(t, catalog.ValidateChangedFiles("guard", []string{"internal/pkg/log.go"}), "cannot include")
	require.ErrorContains(t, catalog.ValidateChangedFiles("web", []string{".github/workflows/ci.yml"}), "cannot include")
	require.ErrorContains(t, catalog.ValidateChangedFiles("web", nil), "no changed files")
}
