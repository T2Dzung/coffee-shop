package security_test

import (
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
