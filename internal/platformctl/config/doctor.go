package config

import (
	"fmt"
	"os"
)

type Doctor struct {
	Loader      Loader
	ProjectRoot string
}

type DoctorReport struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ConfigPath    string              `json:"configPath"`
	Environments  []DoctorEnvironment `json:"environments"`
}

type DoctorEnvironment struct {
	Name           string `json:"name"`
	VarFile        string `json:"terraformVarFile"`
	AWSProfile     string `json:"awsProfile,omitempty"`
	AccountID      string `json:"accountId"`
	Region         string `json:"region"`
	Kubeconfig     string `json:"kubeconfig,omitempty"`
	CredentialMode string `json:"credentialMode"`
}

func (d Doctor) Run(environment string) (DoctorReport, error) {
	source, err := d.Loader.LoadOperator()
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{SchemaVersion: OperatorSchemaVersion, ConfigPath: source.Path}
	selected := map[string]bool{"dev": false, "ci": false, "prod": false}
	if environment == "all" {
		for key := range selected {
			selected[key] = true
		}
	} else if _, ok := selected[environment]; ok {
		selected[environment] = true
	} else {
		return DoctorReport{}, fmt.Errorf("environment must be dev, ci, prod or all")
	}
	if selected["dev"] {
		cfg, loadErr := d.Loader.LoadDev(d.ProjectRoot, "")
		if loadErr != nil {
			return DoctorReport{}, fmt.Errorf("DEV config: %w", loadErr)
		}
		if err := requirePrivateFile(cfg.SSHPrivateKey, "DEV SSH private key"); err != nil {
			return DoctorReport{}, err
		}
		if err := requirePrivateFile(cfg.AnsibleVaultPasswordFile, "DEV Ansible vault password"); err != nil {
			return DoctorReport{}, err
		}
		report.Environments = append(report.Environments, DoctorEnvironment{
			Name: "dev", VarFile: cfg.VarFile, AWSProfile: cfg.AWSProfile, AccountID: cfg.AccountID,
			Region: cfg.Region, Kubeconfig: cfg.Kubeconfig, CredentialMode: "AWS shared config + referenced files",
		})
	}
	if selected["ci"] {
		cfg, loadErr := d.Loader.LoadCI(d.ProjectRoot, "")
		if loadErr != nil {
			return DoctorReport{}, fmt.Errorf("CI config: %w", loadErr)
		}
		if err := requirePrivateFile(cfg.SSHPrivateKey, "CI SSH private key"); err != nil {
			return DoctorReport{}, err
		}
		report.Environments = append(report.Environments, DoctorEnvironment{
			Name: "ci", VarFile: cfg.VarFile, AWSProfile: cfg.AWSProfile, AccountID: cfg.AccountID,
			Region: cfg.Region, CredentialMode: cfg.GitHubAuthMode + " referenced secret file",
		})
	}
	if selected["prod"] {
		cfg, loadErr := d.Loader.LoadProd(d.ProjectRoot, "")
		if loadErr != nil {
			return DoctorReport{}, fmt.Errorf("PROD config: %w", loadErr)
		}
		report.Environments = append(report.Environments, DoctorEnvironment{
			Name: "prod", VarFile: cfg.VarFile, AWSProfile: cfg.AWSProfile, AccountID: cfg.AccountID,
			Region: cfg.Region, Kubeconfig: cfg.Kubeconfig, CredentialMode: "AWS shared config/OIDC",
		})
	}
	return report, nil
}

func requirePrivateFile(path, description string) error {
	if path == "" {
		return fmt.Errorf("%s path is required", description)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", description, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a regular file inaccessible by group or others", description)
	}
	return nil
}
