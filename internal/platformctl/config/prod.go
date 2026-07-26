package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

var (
	accountPattern  = regexp.MustCompile(`^[0-9]{12}$`)
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	revisionPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// Prod contains operator inputs only. Runtime-derived values remain outputs of
// Terraform/AWS/Kubernetes and are never copied into this configuration.
type Prod struct {
	ProjectRoot        string
	VarFile            string
	ProjectName        string
	Environment        string
	Region             string
	AccountID          string
	GitHubRepository   string
	GitOpsRepository   string
	GitOpsRevision     string
	PublicAccessCIDRs  []string
	NodeInstanceTypes  []string
	NodeDesiredSize    int
	NodeDiskGiB        int
	RDSInstanceClass   string
	RDSStorageGiB      int
	RDSEngineVersion   string
	ClusterVersion     string
	EBSAddonVersion    string
	CloudWatchVersion  string
	StateBucket        string
	BackendRoleARN     string
	BootstrapStateKey  string
	FoundationStateKey string
	Kubeconfig         string
	PollAttempts       int
	ReleaseAttempts    int
	WaitTimeout        string
	AutoApprove        bool
}

type Loader struct {
	LookupEnv func(string) (string, bool)
	HomeDir   func() (string, error)
}

func NewLoader() Loader {
	return Loader{LookupEnv: os.LookupEnv, HomeDir: os.UserHomeDir}
}

func (l Loader) LoadProd(projectRoot, varFile string) (Prod, error) {
	if l.LookupEnv == nil {
		l.LookupEnv = os.LookupEnv
	}
	if l.HomeDir == nil {
		l.HomeDir = os.UserHomeDir
	}
	if varFile == "" {
		if value, ok := l.LookupEnv("PROD_VAR_FILE"); ok {
			varFile = value
		} else {
			varFile = filepath.Join(projectRoot, "infrastructure", "terraform", "envs", "prod", "terraform.tfvars")
		}
	}

	attrs, err := parseAttributes(varFile)
	if err != nil {
		return Prod{}, err
	}
	home, err := l.HomeDir()
	if err != nil {
		return Prod{}, fmt.Errorf("resolve home directory: %w", err)
	}

	cfg := Prod{
		ProjectRoot:        projectRoot,
		VarFile:            varFile,
		ProjectName:        stringValue(attrs, "project_name", "coffeeshop"),
		Environment:        stringValue(attrs, "environment", "prod"),
		Region:             stringValue(attrs, "aws_region", ""),
		AccountID:          stringValue(attrs, "expected_aws_account_id", ""),
		GitHubRepository:   stringValue(attrs, "github_repository", ""),
		PublicAccessCIDRs:  stringsValue(attrs, "cluster_endpoint_public_access_cidrs", nil),
		NodeInstanceTypes:  stringsValue(attrs, "node_instance_types", []string{"t3.medium"}),
		NodeDesiredSize:    intValue(attrs, "node_desired_size", 3),
		NodeDiskGiB:        intValue(attrs, "node_disk_size", 20),
		RDSInstanceClass:   stringValue(attrs, "rds_instance_class", "db.t4g.micro"),
		RDSStorageGiB:      intValue(attrs, "rds_allocated_storage", 20),
		RDSEngineVersion:   stringValue(attrs, "rds_engine_version", "16.14"),
		ClusterVersion:     stringValue(attrs, "cluster_version", "1.35"),
		EBSAddonVersion:    stringValue(attrs, "ebs_csi_addon_version", "v1.62.0-eksbuild.1"),
		CloudWatchVersion:  stringValue(attrs, "cloudwatch_observability_addon_version", "v6.4.0-eksbuild.1"),
		BootstrapStateKey:  "prod/bootstrap.tfstate",
		FoundationStateKey: "prod/foundation.tfstate",
		PollAttempts:       60,
		ReleaseAttempts:    360,
		WaitTimeout:        "20m",
	}

	cfg.Region = envString(l.LookupEnv, "PROD_EXPECTED_AWS_REGION", cfg.Region)
	cfg.AccountID = envString(l.LookupEnv, "PROD_EXPECTED_AWS_ACCOUNT_ID", cfg.AccountID)
	cfg.ProjectName = envString(l.LookupEnv, "PROD_PROJECT_NAME", cfg.ProjectName)
	cfg.Environment = envString(l.LookupEnv, "PROD_ENVIRONMENT", cfg.Environment)
	cfg.GitHubRepository = envString(l.LookupEnv, "PROD_GITHUB_REPOSITORY", cfg.GitHubRepository)
	cfg.GitOpsRepository = envString(l.LookupEnv, "PROD_GITOPS_REPO_URL", "https://github.com/"+cfg.GitHubRepository+".git")
	cfg.GitOpsRevision = envString(l.LookupEnv, "PROD_GITOPS_REVISION", "HEAD")
	cfg.StateBucket = envString(l.LookupEnv, "PROD_STATE_BUCKET_NAME", cfg.ProjectName+"-terraform-state-"+cfg.AccountID)
	cfg.BackendRoleARN = envString(l.LookupEnv, "PROD_BACKEND_ROLE_ARN",
		"arn:aws:iam::"+cfg.AccountID+":role/"+cfg.ProjectName+"-terraform-backend-role")
	cfg.BootstrapStateKey = envString(l.LookupEnv, "PROD_BOOTSTRAP_STATE_KEY", cfg.BootstrapStateKey)
	cfg.FoundationStateKey = envString(l.LookupEnv, "PROD_FOUNDATION_STATE_KEY", cfg.FoundationStateKey)
	cfg.Kubeconfig = envString(l.LookupEnv, "PROD_KUBECONFIG", filepath.Join(home, ".kube", cfg.ProjectName+"-prod.yaml"))
	cfg.WaitTimeout = envString(l.LookupEnv, "PROD_WAIT_TIMEOUT", cfg.WaitTimeout)
	cfg.PollAttempts = envInt(l.LookupEnv, "PROD_POLL_ATTEMPTS", cfg.PollAttempts)
	cfg.ReleaseAttempts = envInt(l.LookupEnv, "PROD_RELEASE_POLL_ATTEMPTS", cfg.ReleaseAttempts)
	cfg.AutoApprove = envString(l.LookupEnv, "PROD_AUTO_APPROVE", "false") == "true"

	if err := cfg.Validate(); err != nil {
		return Prod{}, err
	}
	return cfg, nil
}

func (c Prod) Validate() error {
	var problems []string
	if !accountPattern.MatchString(c.AccountID) {
		problems = append(problems, "expected AWS account ID must contain 12 digits")
	}
	if c.Region == "" {
		problems = append(problems, "AWS Region is required")
	}
	if !repoPattern.MatchString(c.GitHubRepository) {
		problems = append(problems, "GitHub repository must use owner/repository form")
	}
	if !revisionPattern.MatchString(c.GitOpsRevision) {
		problems = append(problems, "GitOps revision contains unsupported characters")
	}
	if len(c.PublicAccessCIDRs) == 0 {
		problems = append(problems, "at least one EKS public-access CIDR is required")
	}
	for _, cidr := range c.PublicAccessCIDRs {
		if cidr == "0.0.0.0/0" {
			problems = append(problems, "EKS public-access CIDR cannot be 0.0.0.0/0")
		}
	}
	if len(c.NodeInstanceTypes) == 0 || c.NodeDesiredSize < 1 || c.NodeDiskGiB < 1 {
		problems = append(problems, "node instance type, desired size and disk size must be positive")
	}
	if c.PollAttempts < 1 || c.ReleaseAttempts < 1 {
		problems = append(problems, "poll attempts must be positive")
	}
	return errors.Join(stringsToErrors(problems)...)
}

func parseAttributes(path string) (map[string]*hcl.Attribute, error) {
	parser := hclparse.NewParser()
	file, diagnostics := parser.ParseHCLFile(path)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse %s: %s", path, diagnostics.Error())
	}
	attrs, diagnostics := file.Body.JustAttributes()
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("decode attributes from %s: %s", path, diagnostics.Error())
	}
	return attrs, nil
}

func stringValue(attrs map[string]*hcl.Attribute, name, fallback string) string {
	attr, ok := attrs[name]
	if !ok {
		return fallback
	}
	var value string
	if diagnostics := gohcl.DecodeExpression(attr.Expr, nil, &value); diagnostics.HasErrors() {
		return fallback
	}
	return value
}

func stringsValue(attrs map[string]*hcl.Attribute, name string, fallback []string) []string {
	attr, ok := attrs[name]
	if !ok {
		return fallback
	}
	var value []string
	if diagnostics := gohcl.DecodeExpression(attr.Expr, nil, &value); diagnostics.HasErrors() {
		return fallback
	}
	return value
}

func intValue(attrs map[string]*hcl.Attribute, name string, fallback int) int {
	attr, ok := attrs[name]
	if !ok {
		return fallback
	}
	var value int
	if diagnostics := gohcl.DecodeExpression(attr.Expr, nil, &value); diagnostics.HasErrors() {
		return fallback
	}
	return value
}

func envString(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(lookup func(string) (string, bool), key string, fallback int) int {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func stringsToErrors(values []string) []error {
	result := make([]error, 0, len(values))
	for _, value := range values {
		result = append(result, errors.New(value))
	}
	return result
}
