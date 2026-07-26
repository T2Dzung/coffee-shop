package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const OperatorSchemaVersion = 1

// OperatorFile stores local, non-secret operator choices. Secret material stays in
// referenced mode-0600 files or native AWS/GitHub credential stores.
type OperatorFile struct {
	SchemaVersion int                  `yaml:"schemaVersion" json:"schemaVersion"`
	Environments  OperatorEnvironments `yaml:"environments" json:"environments"`
}

type OperatorEnvironments struct {
	Dev  OperatorEnvironment `yaml:"dev" json:"dev"`
	CI   OperatorEnvironment `yaml:"ci" json:"ci"`
	Prod OperatorEnvironment `yaml:"prod" json:"prod"`
}

type OperatorEnvironment struct {
	VarFile                  string             `yaml:"terraformVarFile,omitempty" json:"terraformVarFile,omitempty"`
	AWSProfile               string             `yaml:"awsProfile,omitempty" json:"awsProfile,omitempty"`
	SSHPrivateKeyFile        string             `yaml:"sshPrivateKeyFile,omitempty" json:"sshPrivateKeyFile,omitempty"`
	AnsibleVaultPasswordFile string             `yaml:"ansibleVaultPasswordFile,omitempty" json:"ansibleVaultPasswordFile,omitempty"`
	Kubeconfig               string             `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty"`
	GitHubAuth               OperatorGitHubAuth `yaml:"githubAuth,omitempty" json:"githubAuth,omitempty"`
	MaxRunners               int                `yaml:"maxRunners,omitempty" json:"maxRunners,omitempty"`
}

type OperatorGitHubAuth struct {
	Mode              string `yaml:"mode,omitempty" json:"mode,omitempty"`
	AppID             string `yaml:"appId,omitempty" json:"appId,omitempty"`
	InstallationID    string `yaml:"installationId,omitempty" json:"installationId,omitempty"`
	AppPrivateKeyFile string `yaml:"appPrivateKeyFile,omitempty" json:"appPrivateKeyFile,omitempty"`
	PersonalTokenFile string `yaml:"personalTokenFile,omitempty" json:"personalTokenFile,omitempty"`
}

type OperatorSource struct {
	Path     string
	Explicit bool
	File     OperatorFile
}

func (l Loader) LoadOperator() (OperatorSource, error) {
	if l.LookupEnv == nil {
		l.LookupEnv = os.LookupEnv
	}
	if l.HomeDir == nil {
		l.HomeDir = os.UserHomeDir
	}
	path := strings.TrimSpace(l.OperatorConfigPath)
	explicit := path != ""
	if !explicit {
		if value, ok := l.LookupEnv("PLATFORMCTL_OPERATOR_CONFIG"); ok && strings.TrimSpace(value) != "" {
			path, explicit = value, true
		}
	}
	if path == "" {
		home, err := l.HomeDir()
		if err != nil {
			return OperatorSource{}, fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, ".config", "go-coffeeshop", "operator.yaml")
	}
	path, err := expandPath(path, "")
	if err != nil {
		return OperatorSource{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return OperatorSource{Path: path}, nil
	}
	if err != nil {
		return OperatorSource{}, fmt.Errorf("read operator config %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file OperatorFile
	if err := decoder.Decode(&file); err != nil {
		return OperatorSource{}, fmt.Errorf("decode operator config %s: %w", path, err)
	}
	if file.SchemaVersion != OperatorSchemaVersion {
		return OperatorSource{}, fmt.Errorf("operator config schemaVersion must be %d", OperatorSchemaVersion)
	}
	base := filepath.Dir(path)
	for _, environment := range []*OperatorEnvironment{
		&file.Environments.Dev, &file.Environments.CI, &file.Environments.Prod,
	} {
		for target, value := range map[*string]string{
			&environment.VarFile:                      environment.VarFile,
			&environment.SSHPrivateKeyFile:            environment.SSHPrivateKeyFile,
			&environment.AnsibleVaultPasswordFile:     environment.AnsibleVaultPasswordFile,
			&environment.Kubeconfig:                   environment.Kubeconfig,
			&environment.GitHubAuth.AppPrivateKeyFile: environment.GitHubAuth.AppPrivateKeyFile,
			&environment.GitHubAuth.PersonalTokenFile: environment.GitHubAuth.PersonalTokenFile,
		} {
			if value == "" {
				continue
			}
			resolved, resolveErr := expandPath(value, base)
			if resolveErr != nil {
				return OperatorSource{}, resolveErr
			}
			*target = resolved
		}
	}
	return OperatorSource{Path: path, Explicit: explicit, File: file}, nil
}

func expandPath(path, relativeTo string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %s: %w", path, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if !filepath.IsAbs(path) && relativeTo != "" {
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path), nil
}

func readSecretFile(path, description string) (string, error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s file: %w", description, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s file is a directory", description)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s file %s must not be accessible by group or others", description, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", description, err)
	}
	return strings.TrimSpace(string(data)), nil
}
