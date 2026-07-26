package validation

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var fullActionSHA = regexp.MustCompile(`^[^@\s]+@[0-9a-fA-F]{40}$`)

type workflowPolicyInput struct {
	UsesSecretsInherit                bool     `json:"uses_secrets_inherit"`
	UnpinnedActions                   []string `json:"unpinned_actions"`
	PullRequestSelfHosted             bool     `json:"pull_request_self_hosted"`
	CandidateBuildWithoutECRPreflight bool     `json:"candidate_build_without_ecr_preflight"`
	CandidatePreflightNotHosted       bool     `json:"candidate_preflight_not_hosted"`
	CandidateBuildMissingToolchain    bool     `json:"candidate_build_missing_toolchain"`
	ARCBuildUsesAWSCLI                bool     `json:"arc_build_uses_aws_cli"`
}

func normalizeWorkflow(path string) (workflowPolicyInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workflowPolicyInput{}, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return workflowPolicyInput{}, fmt.Errorf("decode workflow %s: %w", path, err)
	}
	_, pullRequest := asMap(document["on"])["pull_request"]
	result := workflowPolicyInput{}
	walkWorkflow(document, pullRequest, &result)
	normalizeCandidateContracts(document, &result)
	return result, nil
}

func normalizeCandidateContracts(document map[string]any, result *workflowPolicyInput) {
	jobs := asMap(document["jobs"])
	if build, ok := jobs["build-candidate"]; ok {
		buildJob := asMap(build)
		result.CandidateBuildWithoutECRPreflight = !containsString(buildJob["needs"], "preflight-candidate-ecr")
		preflightJob := asMap(jobs["preflight-candidate-ecr"])
		result.CandidatePreflightNotHosted = len(preflightJob) == 0 || isSelfHosted(preflightJob["runs-on"])
		result.CandidateBuildMissingToolchain = !containsRunText(buildJob, "toolchain verify --profile candidate-runner")
	}
	if name, _ := document["name"].(string); name == "Build immutable component" {
		result.ARCBuildUsesAWSCLI = containsRunText(document, "aws ")
	}
}

func containsString(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return typed == expected
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == expected {
				return true
			}
		}
	}
	return false
}

func containsRunText(value any, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "run" {
				if text, ok := child.(string); ok && strings.Contains(text, expected) {
					return true
				}
			}
			if containsRunText(child, expected) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsRunText(child, expected) {
				return true
			}
		}
	}
	return false
}

func walkWorkflow(value any, pullRequest bool, result *workflowPolicyInput) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "secrets":
				if text, ok := child.(string); ok && text == "inherit" {
					result.UsesSecretsInherit = true
				}
			case "uses":
				if reference, ok := child.(string); ok &&
					!strings.HasPrefix(reference, "./") &&
					!strings.HasPrefix(reference, "docker://") &&
					!fullActionSHA.MatchString(reference) {
					result.UnpinnedActions = append(result.UnpinnedActions, reference)
				}
			case "runs-on":
				if pullRequest && isSelfHosted(child) {
					result.PullRequestSelfHosted = true
				}
			}
			walkWorkflow(child, pullRequest, result)
		}
	case []any:
		for _, child := range typed {
			walkWorkflow(child, pullRequest, result)
		}
	}
}

func asMap(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func isSelfHosted(value any) bool {
	switch typed := value.(type) {
	case string:
		return !strings.HasPrefix(typed, "ubuntu-") &&
			!strings.HasPrefix(typed, "windows-") &&
			!strings.HasPrefix(typed, "macos-")
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == "self-hosted" {
				return true
			}
		}
	}
	return false
}
