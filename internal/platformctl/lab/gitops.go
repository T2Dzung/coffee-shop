package lab

import (
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// RenderGitOps produces reviewable Applications, not a live adoption. The caller
// must verify that the revision is published and the registry digests are available.
func RenderGitOps(cfg Config, revision string, coreLock, ordersLock []byte) ([]byte, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(revision) {
		return nil, fmt.Errorf("gitops revision must be a full commit SHA")
	}
	var result []byte
	for _, profile := range []struct {
		name, namespace string
		lock            []byte
		allowed         map[string]Image
	}{
		{"core", cfg.Namespace, coreLock, cfg.Images},
		{"orders", dataNamespace, ordersLock, cfg.OrderImages},
	} {
		var lock struct {
			Images []Image `yaml:"images"`
		}
		if err := yaml.Unmarshal(profile.lock, &lock); err != nil {
			return nil, fmt.Errorf("%s lock: %w", profile.name, err)
		}
		if len(lock.Images) != len(profile.allowed) {
			return nil, fmt.Errorf("%s lock: incomplete image set", profile.name)
		}
		seen := map[string]bool{}
		images := []string{}
		for _, image := range lock.Images {
			expected, ok := profile.allowed[image.NewName]
			if !ok || seen[image.NewName] || (image.Name != expected.Name && image.Name != image.NewName) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image.Digest) {
				return nil, fmt.Errorf("%s lock: invalid, duplicate or foreign image", profile.name)
			}
			seen[image.NewName] = true
			// The migration Job remains CLI-owned and is not rendered by this source.
			if expected.Name == "go-coffeeshop-migrate" {
				continue
			}
			// The referenced overlay has already renamed images to registry paths.
			images = append(images, image.NewName+"="+image.NewName+"@"+image.Digest)
		}
		sort.Strings(images)
		app := map[string]any{
			"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
			"metadata": map[string]any{"name": "coffeeshop-lab-" + profile.name, "namespace": "argocd"},
			"spec": map[string]any{
				"project":     "coffeeshop-lab",
				"source":      map[string]any{"repoURL": "https://github.com/T2Dzung/coffee-shop", "targetRevision": revision, "path": "infrastructure/iximiuz/gitops/" + profile.name, "kustomize": map[string]any{"images": images}},
				"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": profile.namespace},
				// Manual first sync. No namespace creation, automatic prune or deletion finalizer.
				"syncPolicy": map[string]any{"syncOptions": []string{"FailOnSharedResource=true"}},
			},
		}
		data, err := yaml.Marshal(app)
		if err != nil {
			return nil, err
		}
		if len(result) > 0 {
			result = append(result, []byte("---\n")...)
		}
		result = append(result, data...)
	}
	return result, nil
}
