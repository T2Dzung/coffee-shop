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
	UsesSecretsInherit    bool     `json:"uses_secrets_inherit"`
	UnpinnedActions       []string `json:"unpinned_actions"`
	PullRequestSelfHosted bool     `json:"pull_request_self_hosted"`
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
	return result, nil
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
