package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type DeniedError struct {
	Namespace string
	Output    string
}

func (e DeniedError) Error() string {
	return fmt.Sprintf("policy %s denied input: %s", e.Namespace, strings.TrimSpace(e.Output))
}

type Evaluator struct {
	Runner      command.Runner
	ProjectRoot string
}

func (e Evaluator) Terraform(ctx context.Context, policyName, inputPath string) error {
	if policyName != "reconcile" && policyName != "teardown" &&
		policyName != "ci-reconcile" && policyName != "ci-teardown" {
		return fmt.Errorf("unsupported Terraform policy %q", policyName)
	}
	namespace := "terraform." + strings.ReplaceAll(policyName, "-", "_")
	return e.File(ctx, namespace, filepath.Join(e.ProjectRoot, "policy", "terraform"), inputPath)
}

func (e Evaluator) KubernetesProd(ctx context.Context, inputPath string) error {
	return e.File(ctx, "kubernetes.prod", filepath.Join(e.ProjectRoot, "policy", "kubernetes"), inputPath)
}

func (e Evaluator) Workflow(ctx context.Context, normalized any) error {
	return e.JSON(ctx, "workflows.security", filepath.Join(e.ProjectRoot, "policy", "workflows"), normalized)
}

func (e Evaluator) Config(ctx context.Context, normalized any) error {
	return e.JSON(ctx, "config.environment", filepath.Join(e.ProjectRoot, "policy", "config"), normalized)
}

func (e Evaluator) JSON(ctx context.Context, namespace, policyPath string, input any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode %s policy input: %w", namespace, err)
	}
	file, err := os.CreateTemp("", "platformctl-policy-*.json")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return e.File(ctx, namespace, policyPath, path)
}

func (e Evaluator) File(ctx context.Context, namespace, policyPath, inputPath string) error {
	result, err := e.Runner.Run(ctx, command.Request{
		Name: "conftest",
		Args: []string{
			"test",
			"--policy", policyPath,
			"--namespace", namespace,
			"--output", "json",
			inputPath,
		},
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		if result.ExitCode == 1 {
			return DeniedError{Namespace: namespace, Output: result.Stdout + result.Stderr}
		}
		return fmt.Errorf("evaluate %s: %w", namespace, err)
	}
	return nil
}

func (e Evaluator) Verify(ctx context.Context) error {
	_, err := e.Runner.Run(ctx, command.Request{
		Name: "conftest",
		Args: []string{
			"verify",
			"--policy", filepath.Join(e.ProjectRoot, "policy"),
		},
		Timeout: 2 * time.Minute,
		Stream:  true,
	})
	return err
}
