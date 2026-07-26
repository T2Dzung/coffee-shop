package toolchain

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

type Catalog struct {
	SchemaVersion int                `yaml:"schemaVersion"`
	Tools         map[string]Tool    `yaml:"tools"`
	Profiles      map[string]Profile `yaml:"profiles"`
}

type Tool struct {
	Version          string `yaml:"version" json:"version"`
	LinuxAMD64SHA256 string `yaml:"linuxAmd64Sha256" json:"linux_amd64_sha256,omitempty"`
}

type Profile struct {
	RequiredCommands []string `yaml:"requiredCommands" json:"required_commands"`
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
	for name, profile := range catalog.Profiles {
		if name == "" || len(profile.RequiredCommands) == 0 {
			return Catalog{}, fmt.Errorf("toolchain profile %q is incomplete", name)
		}
		seen := map[string]struct{}{}
		for _, command := range profile.RequiredCommands {
			if command == "" {
				return Catalog{}, fmt.Errorf("toolchain profile %q contains an empty command", name)
			}
			if _, exists := seen[command]; exists {
				return Catalog{}, fmt.Errorf("toolchain profile %q duplicates command %q", name, command)
			}
			seen[command] = struct{}{}
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

func (c Catalog) VerifyProfile(name string, lookup func(string) (string, error)) error {
	profile, ok := c.Profiles[name]
	if !ok {
		return fmt.Errorf("toolchain profile %q is not in the catalog", name)
	}
	missing := make([]string, 0)
	for _, command := range profile.RequiredCommands {
		if _, err := lookup(command); err != nil {
			missing = append(missing, command)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("toolchain profile %q is missing commands: %v", name, missing)
	}
	return nil
}
