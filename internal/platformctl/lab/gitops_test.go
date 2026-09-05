package lab

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRenderGitOps(t *testing.T) {
	cfg := testConfig(t)
	lock := func(allowed map[string]Image) []byte {
		images := []Image{}
		for _, image := range allowed {
			image.Digest = "sha256:" + strings.Repeat("a", 64)
			images = append(images, image)
		}
		data, err := yaml.Marshal(map[string]any{"images": images})
		require.NoError(t, err)
		return data
	}
	core, orders := lock(cfg.Images), lock(cfg.OrderImages)
	data, err := RenderGitOps(cfg, strings.Repeat("b", 40), core, orders)
	require.NoError(t, err)
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	count := 0
	for {
		var app map[string]any
		err := decoder.Decode(&app)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		meta := app["metadata"].(map[string]any)
		require.NotContains(t, meta, "finalizers")
		spec := app["spec"].(map[string]any)
		require.Equal(t, "coffeeshop-lab", spec["project"])
		policy := spec["syncPolicy"].(map[string]any)
		require.NotContains(t, policy, "automated")
		source := spec["source"].(map[string]any)
		require.Equal(t, strings.Repeat("b", 40), source["targetRevision"])
		images := source["kustomize"].(map[string]any)["images"].([]any)
		expected := 2
		if count == 1 {
			expected = 3
		}
		require.Len(t, images, expected)
		for _, image := range images {
			require.Contains(t, image, "@sha256:")
			require.NotContains(t, image, "migrate")
			require.NotContains(t, image, ":lab-")
		}
		count++
	}
	require.Equal(t, 2, count)
	for _, tc := range []struct {
		name, rev    string
		core, orders []byte
	}{
		{"floating revision", "main", core, orders},
		{"empty images", strings.Repeat("b", 40), []byte("images: []"), orders},
		{"foreign registry", strings.Repeat("b", 40), []byte(strings.ReplaceAll(string(core), "registry.iximiuz.com", "other.example")), orders},
		{"invalid digest", strings.Repeat("b", 40), []byte(strings.ReplaceAll(string(core), "sha256:", "invalid:")), orders},
		{"invalid YAML", strings.Repeat("b", 40), core, []byte("[")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := RenderGitOps(cfg, tc.rev, tc.core, tc.orders)
			require.Error(t, err)
			require.Empty(t, result)
		})
	}
}
