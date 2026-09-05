package lab

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

func TestRenderGitOpsRenderedDigests(t *testing.T) {
	cfg := testConfig(t)
	makeLock := func(allowed map[string]Image) []byte {
		images := []Image{}
		for _, image := range allowed {
			image.Digest = "sha256:" + strings.Repeat("a", 64)
			images = append(images, image)
		}
		data, err := yaml.Marshal(map[string]any{"images": images})
		require.NoError(t, err)
		return data
	}
	appData, err := RenderGitOps(cfg, strings.Repeat("b", 40), makeLock(cfg.Images), makeLock(cfg.OrderImages))
	require.NoError(t, err)
	appDecoder := yaml.NewDecoder(strings.NewReader(string(appData)))
	for _, profile := range []struct {
		name   string
		images map[string]Image
	}{{"core", cfg.Images}, {"orders", cfg.OrderImages}} {
		t.Run(profile.name, func(t *testing.T) {
			overrides := []map[string]string{}
			var app map[string]any
			require.NoError(t, appDecoder.Decode(&app))
			images := app["spec"].(map[string]any)["source"].(map[string]any)["kustomize"].(map[string]any)["images"].([]any)
			for _, raw := range images {
				name, target, ok := strings.Cut(raw.(string), "=")
				require.True(t, ok)
				repo, digest, ok := strings.Cut(target, "@")
				require.True(t, ok)
				overrides = append(overrides, map[string]string{"name": name, "newName": repo, "digest": digest})
			}
			dir := t.TempDir()
			resource, err := filepath.Rel(dir, filepath.Join(cfg.Root, "infrastructure/iximiuz/gitops", profile.name))
			require.NoError(t, err)
			data, err := yaml.Marshal(map[string]any{"apiVersion": "kustomize.config.k8s.io/v1beta1", "kind": "Kustomization", "resources": []string{resource}, "images": overrides})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), data, 0600))
			result, err := (command.OSRunner{}).Run(context.Background(), command.Request{Name: "kubectl", Args: []string{"kustomize", dir}, Timeout: 30 * time.Second})
			require.NoError(t, err, result.Stderr)
			decoder := yaml.NewDecoder(strings.NewReader(result.Stdout))
			count := 0
			for {
				var obj map[string]any
				err := decoder.Decode(&obj)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				if obj["kind"] != "Deployment" {
					continue
				}
				pod := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
				for _, raw := range pod["containers"].([]any) {
					require.Contains(t, raw.(map[string]any)["image"], "@sha256:"+strings.Repeat("a", 64))
					count++
				}
			}
			require.NotZero(t, count)
		})
	}
}

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
