package ci

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
)

func TestCandidateRepositoryNamesUseCatalogImageRepositories(t *testing.T) {
	t.Parallel()
	catalog := component.Catalog{Components: []component.Component{
		{Name: "web", ImageRepository: "go-coffeeshop-web"},
		{Name: "guard", ImageRepository: "platform-ownership-guard"},
	}}
	require.Equal(t, []string{
		"coffeeshop-candidate-go-coffeeshop-web",
		"coffeeshop-candidate-platform-ownership-guard",
	}, candidateRepositoryNames(catalog))
}
