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
			{Name: "web", Automatic: true, Paths: []string{"cmd/web/**"}},
			{Name: "guard", Automatic: true, Paths: []string{"guard/**"}},
			{Name: "migrate", Kind: "migration", Paths: []string{"db/**"}},
		},
	}
	require.Equal(t, []string{"web"}, catalog.Select([]string{"cmd/web/main.go"}))
	require.Equal(t, []string{"guard", "web"}, catalog.Select([]string{"go.mod"}))
	require.Empty(t, catalog.Select([]string{"db/migrate.go"}))
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
