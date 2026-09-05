package validation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"gopkg.in/yaml.v3"
)

// GitOps must render exactly the CLI workload set minus Namespace. In particular,
// it must never adopt operator resources, credentials, PVCs or migration Jobs.
func TestIximiuzGitOpsOwnershipBoundary(t *testing.T) {
	root := projectRootFromTest(t)
	projectBytes, err := os.ReadFile(filepath.Join(root, "infrastructure/iximiuz/gitops/appproject.yaml"))
	require.NoError(t, err)
	var project map[string]any
	require.NoError(t, yaml.Unmarshal(projectBytes, &project))
	spec := labMap(t, project["spec"])
	require.Empty(t, spec["clusterResourceWhitelist"])
	require.ElementsMatch(t, []any{
		map[string]any{"group": "apps", "kind": "Deployment"},
		map[string]any{"group": "", "kind": "Service"},
		map[string]any{"group": "", "kind": "ConfigMap"},
		map[string]any{"group": "networking.k8s.io", "kind": "NetworkPolicy"},
	}, spec["namespaceResourceWhitelist"])
	require.ElementsMatch(t, []any{
		map[string]any{"namespace": "coffeeshop-lab", "server": "https://kubernetes.default.svc"},
		map[string]any{"namespace": "coffeeshop-lab-data", "server": "https://kubernetes.default.svc"},
	}, spec["destinations"])
	for _, profile := range []string{"iximiuz-core", "iximiuz-orders"} {
		t.Run(profile, func(t *testing.T) {
			render := func(suffix string) map[string]map[string]any {
				t.Helper()
				path := filepath.Join(root, "infrastructure/k8s/apps/coffeeshop/overlays", profile)
				if suffix == "gitops" {
					path = filepath.Join(root, "infrastructure/iximiuz/gitops", strings.TrimPrefix(profile, "iximiuz-"))
				}
				result, err := (command.OSRunner{}).Run(context.Background(), command.Request{
					Name: "kubectl", Args: []string{"kustomize", path}, Timeout: 30 * time.Second,
				})
				require.NoError(t, err, result.Stderr)
				objects := map[string]map[string]any{}
				decoder := yaml.NewDecoder(strings.NewReader(result.Stdout))
				for {
					var obj map[string]any
					err := decoder.Decode(&obj)
					if err == io.EOF {
						break
					}
					require.NoError(t, err)
					meta := labMap(t, obj["metadata"])
					key := obj["kind"].(string) + "/" + meta["name"].(string)
					require.NotContains(t, objects, key)
					objects[key] = obj
				}
				return objects
			}
			original := render("")
			managed := render("gitops")
			ns := "coffeeshop-lab"
			if profile == "iximiuz-orders" {
				ns = "coffeeshop-lab-data"
			}
			require.Contains(t, original, "Namespace/"+ns)
			delete(original, "Namespace/"+ns)
			require.NotEmpty(t, managed)
			require.Equal(t, original, managed)
			for _, obj := range managed {
				require.Contains(t, []string{"Deployment", "Service", "ConfigMap", "NetworkPolicy"}, obj["kind"])
				require.Equal(t, ns, labMap(t, obj["metadata"])["namespace"])
			}
		})
	}
}
