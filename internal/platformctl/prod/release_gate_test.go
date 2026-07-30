package prod

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromotedGuardArtifactRequiresSevenAppsAndCatalogGuard(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	apps := strings.Repeat("digest: "+digest+"\n", 7)
	guard := "digest: " + digest
	// Protected main can still contain the pre-test-metadata catalog while a
	// newer local platformctl runs setup. The release gate must remain readable
	// across that rollout boundary.
	catalog := `
schemaVersion: 1
components:
  - name: platform-ownership-guard
    kind: operator
    build: go
    moduleRoot: platform-ownership-guard
    package: ./cmd/main.go
    binary: bin/manager
    dockerfile: platform-ownership-guard/Dockerfile
    imageRepository: platform-ownership-guard
    kustomizeImage: controller
    context: platform-ownership-guard
    devOverlay: platform-ownership-guard/config/default
    prodOverlay: platform-ownership-guard/config/prod
    automatic: true
    paths: [platform-ownership-guard/**]
`
	repository, resolved, ready := promotedGuardArtifact(apps, guard, catalog)
	require.True(t, ready)
	require.Equal(t, "platform-ownership-guard", repository)
	require.Equal(t, digest, resolved)
}

func TestPromotedGuardArtifactFailsClosed(t *testing.T) {
	t.Parallel()
	valid := "sha256:" + strings.Repeat("a", 64)
	zero := "sha256:" + strings.Repeat("0", 64)
	tests := []struct {
		name  string
		apps  string
		guard string
	}{
		{name: "application placeholder", apps: strings.Repeat("digest: "+valid+"\n", 6) + "digest: " + zero, guard: valid},
		{name: "missing application", apps: strings.Repeat("digest: "+valid+"\n", 6), guard: valid},
		{name: "guard placeholder", apps: strings.Repeat("digest: "+valid+"\n", 7), guard: zero},
		{name: "multiple guard digests", apps: strings.Repeat("digest: "+valid+"\n", 7), guard: fmt.Sprintf("%s\n%s", valid, valid)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, ready := promotedGuardArtifact(test.apps, test.guard, "schemaVersion: 1\ncomponents: []")
			require.False(t, ready)
		})
	}
}
