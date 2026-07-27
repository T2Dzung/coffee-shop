package component

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Catalog struct {
	SchemaVersion int         `yaml:"schemaVersion"`
	SharedPaths   []string    `yaml:"sharedPaths"`
	Components    []Component `yaml:"components"`
}

type Component struct {
	Name            string   `yaml:"name" json:"name"`
	Kind            string   `yaml:"kind" json:"kind"`
	Build           string   `yaml:"build" json:"build"`
	ModuleRoot      string   `yaml:"moduleRoot" json:"module_root"`
	Package         string   `yaml:"package" json:"package"`
	Binary          string   `yaml:"binary" json:"binary"`
	Dockerfile      string   `yaml:"dockerfile" json:"dockerfile"`
	ImageRepository string   `yaml:"imageRepository" json:"image_repository"`
	KustomizeImage  string   `yaml:"kustomizeImage" json:"kustomize_image"`
	Context         string   `yaml:"context" json:"context"`
	DevOverlay      string   `yaml:"devOverlay" json:"dev_overlay"`
	ProdOverlay     string   `yaml:"prodOverlay" json:"prod_overlay"`
	Automatic       bool     `yaml:"automatic" json:"automatic"`
	Paths           []string `yaml:"paths" json:"paths"`
}

func Load(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read component catalog: %w", err)
	}
	return Decode(data)
}

func Decode(data []byte) (Catalog, error) {
	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode component catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("component catalog schemaVersion must be 1")
	}
	if len(c.Components) == 0 {
		return fmt.Errorf("component catalog is empty")
	}
	seen := map[string]struct{}{}
	for _, component := range c.Components {
		if component.Name == "" || component.Kind == "" || component.Build == "" ||
			component.ModuleRoot == "" || component.Package == "" || component.Binary == "" ||
			component.Dockerfile == "" || component.ImageRepository == "" || component.KustomizeImage == "" ||
			component.Context == "" || component.DevOverlay == "" || component.ProdOverlay == "" || len(component.Paths) == 0 {
			return fmt.Errorf("component %q has incomplete build or image metadata", component.Name)
		}
		if component.Kind == "migration" && component.Automatic {
			return fmt.Errorf("migration component %q cannot be selected automatically", component.Name)
		}
		if _, exists := seen[component.Name]; exists {
			return fmt.Errorf("component %q is duplicated", component.Name)
		}
		seen[component.Name] = struct{}{}
	}
	return nil
}

func (c Catalog) Names() []string {
	names := make([]string, 0, len(c.Components))
	for _, component := range c.Components {
		names = append(names, component.Name)
	}
	return names
}

func (c Catalog) Find(name string) (Component, error) {
	for _, candidate := range c.Components {
		if candidate.Name == name {
			return candidate, nil
		}
	}
	return Component{}, fmt.Errorf("component %q is not in the catalog", name)
}

func (c Catalog) Select(changedFiles []string) []string {
	selected := map[string]struct{}{}
	shared := matchesAny(changedFiles, c.SharedPaths)
	for _, candidate := range c.Components {
		if !candidate.Automatic {
			continue
		}
		if (shared && candidate.Kind == "service") || matchesAny(changedFiles, candidate.Paths) {
			selected[candidate.Name] = struct{}{}
		}
	}
	result := make([]string, 0, len(selected))
	for name := range selected {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (c Catalog) FilterKind(names []string, kind string) []string {
	if kind == "" {
		return names
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		entry, err := c.Find(name)
		if err == nil && entry.Kind == kind {
			result = append(result, name)
		}
	}
	return result
}

func (c Catalog) Resolve(names []string, allowMigration bool) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		component, err := c.Find(name)
		if err != nil {
			return nil, err
		}
		if component.Kind == "migration" && !allowMigration {
			return nil, fmt.Errorf("migration component %q requires explicit --allow-migration", name)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func (c Catalog) CandidateRepositoryNames(names []string) ([]string, error) {
	resolved, err := c.Resolve(names, true)
	if err != nil {
		return nil, err
	}
	repositories := make([]string, 0, len(resolved))
	for _, name := range resolved {
		entry, err := c.Find(name)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, CandidateRepositoryName(entry))
	}
	return repositories, nil
}

func CandidateRepositoryName(entry Component) string {
	return "coffeeshop-candidate-" + entry.ImageRepository
}

func JSONNames(names []string) ([]byte, error) {
	return json.Marshal(names)
}

func matchesAny(files, patterns []string) bool {
	for _, file := range files {
		clean := path.Clean(strings.TrimSpace(file))
		for _, pattern := range patterns {
			if matchPath(clean, pattern) {
				return true
			}
		}
	}
	return false
}

func matchPath(file, pattern string) bool {
	pattern = strings.TrimPrefix(path.Clean(pattern), "./")
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "**")
		return strings.HasPrefix(file, prefix)
	}
	matched, err := path.Match(pattern, file)
	return err == nil && matched
}
