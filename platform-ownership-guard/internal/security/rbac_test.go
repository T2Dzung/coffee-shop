package security_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestManagerRoleIsReadOnlyOutsideAuditStatusAndEvents(t *testing.T) {
	path := filepath.Join("..", "..", "config", "rbac", "role.yaml")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open manager role: %v", err)
	}
	defer file.Close()

	var role rbacv1.ClusterRole
	if err := utilyaml.NewYAMLOrJSONDecoder(file, 4096).Decode(&role); err != nil && err != io.EOF {
		t.Fatalf("decode manager role: %v", err)
	}

	if len(role.Rules) == 0 {
		t.Fatal("manager role must contain generated rules")
	}

	foundAuditRead := false
	foundStatusWrite := false
	foundApplicationRead := false
	foundTargetRead := false
	foundEventWrite := false

	for _, rule := range role.Rules {
		if slices.Contains(rule.APIGroups, "*") || slices.Contains(rule.Resources, "*") || slices.Contains(rule.Verbs, "*") {
			t.Fatalf("wildcard RBAC is forbidden: %#v", rule)
		}
		if slices.Contains(rule.Resources, "secrets") {
			t.Fatalf("Secret RBAC is forbidden: %#v", rule)
		}

		for _, resource := range rule.Resources {
			switch resource {
			case "ownershipaudits":
				foundAuditRead = true
				assertOnlyVerbs(t, rule.Verbs, "get", "list", "watch")
			case "ownershipaudits/status":
				foundStatusWrite = true
				assertOnlyVerbs(t, rule.Verbs, "get", "patch", "update")
			case "applications":
				foundApplicationRead = true
				assertOnlyVerbs(t, rule.Verbs, "get")
			case "deployments", "replicasets":
				foundTargetRead = true
				assertOnlyVerbs(t, rule.Verbs, "get", "list", "watch")
			case "events":
				foundEventWrite = true
				if !slices.Contains(rule.APIGroups, "events.k8s.io") {
					t.Fatalf("events resource must belong to events.k8s.io group, got %#v", rule.APIGroups)
				}
				assertOnlyVerbs(t, rule.Verbs, "create", "patch", "update")
			default:
				for _, forbidden := range []string{"create", "update", "patch", "delete", "deletecollection"} {
					if slices.Contains(rule.Verbs, forbidden) {
						t.Fatalf("non-status resource %q has write verb %q", resource, forbidden)
					}
				}
			}
		}
	}

	if !foundAuditRead || !foundStatusWrite || !foundApplicationRead || !foundTargetRead || !foundEventWrite {
		t.Fatalf("expected audit read, status write, and events write rules, got %#v", role.Rules)
	}
}

func TestLeaseRoleIsNamespacedAndRestricted(t *testing.T) {
	rolePath := filepath.Join("..", "..", "config", "rbac", "lease_role.yaml")
	roleBytes, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read lease role: %v", err)
	}

	var role rbacv1.Role
	if err := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(roleBytes), 4096).Decode(&role); err != nil && err != io.EOF {
		t.Fatalf("decode lease role: %v", err)
	}

	if role.Kind != "Role" {
		t.Fatalf("expected lease role Kind to be Role, got %q", role.Kind)
	}
	if role.Name != "lease-role" {
		t.Fatalf("expected lease role Name to be lease-role, got %q", role.Name)
	}
	if role.Namespace != "system" {
		t.Fatalf("expected lease role Namespace to be system, got %q", role.Namespace)
	}

	if len(role.Rules) == 0 {
		t.Fatal("lease role must contain rules")
	}

	foundLeaseRule := false
	foundLeaderEventRule := false
	for _, rule := range role.Rules {
		for _, resource := range rule.Resources {
			switch {
			case resource == "leases" && slices.Contains(rule.APIGroups, "coordination.k8s.io"):
				foundLeaseRule = true
				assertOnlyVerbs(t, rule.Verbs, "get", "list", "watch", "create", "update", "patch")
			case resource == "events" && slices.Contains(rule.APIGroups, ""):
				foundLeaderEventRule = true
				assertOnlyVerbs(t, rule.Verbs, "create", "patch")
			default:
				t.Fatalf("unexpected lease-role rule: groups=%#v resource=%s verbs=%#v", rule.APIGroups, resource, rule.Verbs)
			}
		}
	}

	if !foundLeaseRule {
		t.Fatal("lease role must contain rules for leases resource")
	}
	if !foundLeaderEventRule {
		t.Fatal("lease role must allow namespaced core events for leader-election reporting")
	}

	// Validate lease_role_binding.yaml
	bindingPath := filepath.Join("..", "..", "config", "rbac", "lease_role_binding.yaml")
	bindingBytes, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatalf("read lease role binding: %v", err)
	}

	var binding rbacv1.RoleBinding
	if err := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(bindingBytes), 4096).Decode(&binding); err != nil && err != io.EOF {
		t.Fatalf("decode lease role binding: %v", err)
	}

	if binding.Kind != "RoleBinding" {
		t.Fatalf("expected lease role binding Kind to be RoleBinding, got %q", binding.Kind)
	}
	if binding.Name != "lease-rolebinding" {
		t.Fatalf("expected lease role binding Name to be lease-rolebinding, got %q", binding.Name)
	}
	if binding.Namespace != "system" {
		t.Fatalf("expected lease role binding Namespace to be system, got %q", binding.Namespace)
	}
	if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != "lease-role" {
		t.Fatalf("unexpected roleRef in lease role binding: %#v", binding.RoleRef)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Kind != "ServiceAccount" || binding.Subjects[0].Name != "controller-manager" || binding.Subjects[0].Namespace != "system" {
		t.Fatalf("unexpected subjects in lease role binding: %#v", binding.Subjects)
	}
}

func assertOnlyVerbs(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("verbs mismatch: got %v, want %v", actual, expected)
	}
	for _, verb := range actual {
		if !slices.Contains(expected, verb) {
			t.Fatalf("unexpected verb %q in %v", verb, actual)
		}
	}
}
