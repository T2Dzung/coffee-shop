package terraform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type Client struct {
	Runner           command.Runner
	Dir              string
	DataDir          string
	VarFile          string
	Variables        map[string]string
	BooleanVariables map[string]bool
	Environment      map[string]string
	Redactions       []string
	Timeout          time.Duration
}

type Plan struct {
	BinaryPath  string
	JSONPath    string
	Fingerprint string
	Summary     Summary
}

type Summary struct {
	Create  int `json:"create"`
	Update  int `json:"update"`
	Delete  int `json:"delete"`
	Replace int `json:"replace"`
}

type planDocument struct {
	ResourceChanges []struct {
		Change struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_changes"`
}

func (c Client) Init(ctx context.Context, backendArgs ...string) error {
	args := []string{"-chdir=" + c.Dir, "init", "-reconfigure", "-input=false"}
	args = append(args, backendArgs...)
	_, err := c.run(ctx, true, args...)
	return err
}

func (c Client) Validate(ctx context.Context) error {
	_, err := c.run(ctx, true, "-chdir="+c.Dir, "validate", "-no-color")
	return err
}

func (c Client) CreatePlan(ctx context.Context, parent, name string, destroy bool, targets []string) (Plan, error) {
	if c.Runner == nil {
		return Plan{}, fmt.Errorf("Terraform runner is required")
	}
	dir, err := os.MkdirTemp(parent, "platformctl-"+name+"-")
	if err != nil {
		return Plan{}, fmt.Errorf("create plan directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	binaryPath := filepath.Join(dir, name+".tfplan")
	jsonPath := filepath.Join(dir, name+".json")
	args := []string{"-chdir=" + c.Dir, "plan", "-input=false", "-out=" + binaryPath}
	if c.VarFile != "" {
		args = append(args, "-var-file="+c.VarFile)
	}
	variableNames := make([]string, 0, len(c.Variables))
	for key := range c.Variables {
		variableNames = append(variableNames, key)
	}
	sort.Strings(variableNames)
	for _, key := range variableNames {
		// command.Runner passes an argument array directly to Terraform, so shell
		// quoting is neither required nor correct. For a declared string variable,
		// embedded JSON quotes become part of the value on Terraform 1.15.
		args = append(args, "-var="+key+"="+c.Variables[key])
	}
	booleanNames := make([]string, 0, len(c.BooleanVariables))
	for key := range c.BooleanVariables {
		booleanNames = append(booleanNames, key)
	}
	sort.Strings(booleanNames)
	for _, key := range booleanNames {
		args = append(args, "-var="+key+"="+fmt.Sprintf("%t", c.BooleanVariables[key]))
	}
	if destroy {
		args = append(args, "-destroy")
	}
	for _, target := range targets {
		args = append(args, "-target="+target)
	}
	if _, err := c.run(ctx, true, args...); err != nil {
		cleanup()
		return Plan{}, err
	}
	result, err := c.run(ctx, false, "-chdir="+c.Dir, "show", "-json", binaryPath)
	if err != nil {
		cleanup()
		return Plan{}, err
	}
	if err := os.WriteFile(jsonPath, []byte(result.Stdout), 0o600); err != nil {
		cleanup()
		return Plan{}, fmt.Errorf("write plan JSON: %w", err)
	}
	summary, err := DecodeSummary([]byte(result.Stdout))
	if err != nil {
		cleanup()
		return Plan{}, err
	}
	fingerprint, err := Fingerprint(binaryPath)
	if err != nil {
		cleanup()
		return Plan{}, err
	}
	return Plan{BinaryPath: binaryPath, JSONPath: jsonPath, Fingerprint: fingerprint, Summary: summary}, nil
}

func (c Client) ShowHuman(ctx context.Context, plan Plan) (string, error) {
	result, err := c.run(ctx, false, "-chdir="+c.Dir, "show", "-no-color", plan.BinaryPath)
	return result.Stdout, err
}

func (c Client) Apply(ctx context.Context, plan Plan) error {
	current, err := Fingerprint(plan.BinaryPath)
	if err != nil {
		return err
	}
	if current != plan.Fingerprint {
		return fmt.Errorf("saved plan fingerprint changed after approval")
	}
	_, err = c.run(ctx, true, "-chdir="+c.Dir, "apply", "-input=false", plan.BinaryPath)
	return err
}

func (c Client) DetailedPlan(ctx context.Context) (int, error) {
	result, err := c.run(ctx, true, "-chdir="+c.Dir, "plan", "-input=false", "-detailed-exitcode")
	if err == nil {
		return 0, nil
	}
	if result.ExitCode == 2 {
		return 2, nil
	}
	return result.ExitCode, err
}

func (c Client) Output(ctx context.Context, name string) (string, error) {
	result, err := c.run(ctx, false, "-chdir="+c.Dir, "output", "-raw", name)
	return result.Stdout, err
}

func (c Client) OutputJSON(ctx context.Context, name string) (string, error) {
	result, err := c.run(ctx, false, "-chdir="+c.Dir, "output", "-json", name)
	return result.Stdout, err
}

func (c Client) run(ctx context.Context, stream bool, args ...string) (command.Result, error) {
	environment := map[string]string{"TF_DATA_DIR": c.DataDir}
	for key, value := range c.Environment {
		environment[key] = value
	}
	return c.Runner.Run(ctx, command.Request{
		Name:       "terraform",
		Args:       args,
		Env:        environment,
		Timeout:    c.Timeout,
		Stream:     stream,
		Redactions: c.Redactions,
	})
}

func DecodeSummary(data []byte) (Summary, error) {
	var document planDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return Summary{}, fmt.Errorf("decode Terraform plan JSON: %w", err)
	}
	var summary Summary
	for _, resource := range document.ResourceChanges {
		create := contains(resource.Change.Actions, "create")
		deleteAction := contains(resource.Change.Actions, "delete")
		switch {
		case create && deleteAction:
			summary.Replace++
		case create:
			summary.Create++
		case deleteAction:
			summary.Delete++
		case contains(resource.Change.Actions, "update"):
			summary.Update++
		}
	}
	return summary, nil
}

func Fingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read saved plan: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (p Plan) Cleanup() error {
	if p.BinaryPath == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(p.BinaryPath))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
