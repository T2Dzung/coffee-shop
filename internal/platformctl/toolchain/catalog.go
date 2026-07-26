package toolchain

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Catalog struct {
	SchemaVersion int             `yaml:"schemaVersion"`
	Tools         map[string]Tool `yaml:"tools"`
}

type Tool struct {
	Version          string `yaml:"version" json:"version"`
	LinuxAMD64SHA256 string `yaml:"linuxAmd64Sha256" json:"linux_amd64_sha256,omitempty"`
}

func Load(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read toolchain catalog: %w", err)
	}
	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode toolchain catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 || len(catalog.Tools) == 0 {
		return Catalog{}, fmt.Errorf("toolchain catalog schemaVersion must be 1 and tools cannot be empty")
	}
	for name, tool := range catalog.Tools {
		if name == "" || tool.Version == "" {
			return Catalog{}, fmt.Errorf("toolchain entry %q is incomplete", name)
		}
	}
	return catalog, nil
}

func (c Catalog) Find(name string) (Tool, error) {
	tool, ok := c.Tools[name]
	if !ok {
		return Tool{}, fmt.Errorf("tool %q is not in the toolchain catalog", name)
	}
	return tool, nil
}
