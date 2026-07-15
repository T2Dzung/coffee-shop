package controller

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestGeneratedRBACIsLeastPrivilegeForPhase62(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "rbac", "role.yaml"))
	if err != nil {
		t.Fatalf("reading generated role: %v", err)
	}

	var role rbacv1.ClusterRole
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("decoding generated role: %v", err)
	}

	for _, rule := range role.Rules {
		if slices.Contains(rule.Resources, "*") || slices.Contains(rule.Verbs, "*") || slices.Contains(rule.APIGroups, "*") {
			t.Fatalf("wildcard RBAC rule is not allowed: %+v", rule)
		}
		if slices.Contains(rule.Resources, "coffeeshopservices/finalizers") {
			t.Fatalf("finalizer permission is outside Slice 6.2.2 scope: %+v", rule)
		}
	}

	expected := []struct {
		group    string
		resource string
		verbs    []string
	}{
		{group: "platform.t2dzung.github.io", resource: "coffeeshopservices", verbs: []string{"get", "list", "watch"}},
		{group: "platform.t2dzung.github.io", resource: "coffeeshopservices/status", verbs: []string{"get", "patch"}},
		{group: "apps", resource: "deployments", verbs: []string{"get", "list", "watch", "create", "patch", "delete"}},
		{group: "", resource: "services", verbs: []string{"get", "list", "watch", "create", "patch", "delete"}},
		{group: "events.k8s.io", resource: "events", verbs: []string{"create", "patch"}},
	}
	if len(role.Rules) != len(expected) {
		t.Fatalf("generated role has %d rules, want exactly %d: %+v", len(role.Rules), len(expected), role.Rules)
	}

	for _, want := range expected {
		if !roleContainsRule(role.Rules, want.group, want.resource, want.verbs) {
			t.Errorf("generated role is missing %q/%q verbs %v", want.group, want.resource, want.verbs)
		}
	}
}

func roleContainsRule(rules []rbacv1.PolicyRule, group, resource string, verbs []string) bool {
	for _, rule := range rules {
		if len(rule.APIGroups) != 1 || len(rule.Resources) != 1 ||
			rule.APIGroups[0] != group || rule.Resources[0] != resource || len(rule.Verbs) != len(verbs) {
			continue
		}
		for _, verb := range verbs {
			if !slices.Contains(rule.Verbs, verb) {
				return false
			}
		}
		return true
	}
	return false
}
