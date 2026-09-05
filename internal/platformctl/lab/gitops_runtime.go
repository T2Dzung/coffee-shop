package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (e Engine) gitops(ctx context.Context) error {
	installed, err := e.ordersPresent(ctx)
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("GitOps adoption requires completed orders profile")
	}
	core, err := e.Client.Remote(ctx, "cat", path.Join(e.Config.Workspace, "runtime-core/kustomization.yaml"))
	if err != nil {
		return err
	}
	orders, err := e.Client.Remote(ctx, "cat", path.Join(e.Config.Workspace, "runtime-orders/kustomization.yaml"))
	if err != nil {
		return err
	}
	apps, err := RenderGitOps(e.Config, e.Config.Revision, []byte(core), []byte(orders))
	if err != nil {
		return err
	}
	var versions struct {
		Argo string `yaml:"argocd_version"`
	}
	if err = readYAML(e.Config.Root, "infrastructure/ansible/playbooks/group_vars/all/versions.yml", &versions); err != nil {
		return err
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(versions.Argo) {
		return fmt.Errorf("invalid pinned Argo version")
	}
	if err = e.step("argocd-namespace", func() error {
		raw, err := e.Client.Remote(ctx, "kubectl", "get", "namespace", "argocd", "--ignore-not-found", "-o", "json")
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) != "" {
			var ns struct {
				Metadata struct{ Labels map[string]string }
			}
			if err = json.Unmarshal([]byte(raw), &ns); err != nil {
				return err
			}
			if ns.Metadata.Labels["app.kubernetes.io/managed-by"] != "platformctl-lab" {
				return fmt.Errorf("argocd namespace is not lab-owned; refusing adoption")
			}
			return nil
		}
		_, err = e.Client.RemoteInput(ctx, strings.NewReader("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: argocd\n  labels:\n    app.kubernetes.io/managed-by: platformctl-lab\n"), "kubectl", "apply", "-f", "-")
		return err
	}); err != nil {
		return err
	}
	if err = e.step("argocd-install", func() error {
		_, err := e.Client.Remote(ctx, "kubectl", "apply", "--server-side", "-n", "argocd", "-f", "https://raw.githubusercontent.com/argoproj/argo-cd/"+versions.Argo+"/manifests/install.yaml")
		return err
	}); err != nil {
		return err
	}
	for _, file := range []string{"config.yaml", "appproject.yaml"} {
		data, err := os.ReadFile(filepath.Join(e.Config.Root, "infrastructure/iximiuz/gitops", file))
		if err != nil {
			return err
		}
		if _, err = e.Client.RemoteInput(ctx, bytes.NewReader(data), "kubectl", "apply", "-f", "-"); err != nil {
			return err
		}
	}
	e.Client.RemoteTimeout = 10 * time.Minute
	for _, target := range []string{"deployment/argocd-repo-server", "statefulset/argocd-application-controller"} {
		if err = e.step("argocd-ready", func() error {
			_, err := e.Client.Remote(ctx, "kubectl", "-n", "argocd", "rollout", "status", target, "--timeout=480s")
			return err
		}); err != nil {
			return err
		}
	}
	if err = e.step("gitops-applications", func() error {
		_, err := e.Client.RemoteInput(ctx, bytes.NewReader(apps), "kubectl", "apply", "-f", "-")
		return err
	}); err != nil {
		return err
	}
	// Explicit first sync, no pruning and no force/replace of live resources.
	operation, err := json.Marshal(map[string]any{"operation": map[string]any{"sync": map[string]any{"revision": e.Config.Revision, "prune": false}}})
	if err != nil {
		return err
	}
	for _, profile := range []string{"core", "orders"} {
		if _, err = e.Client.Remote(ctx, "kubectl", "-n", "argocd", "patch", "application", "coffeeshop-lab-"+profile, "--type=merge", "-p", string(operation)); err != nil {
			return err
		}
	}
	if err = e.step("gitops-health", func() error { return e.gitopsHealth(ctx) }); err != nil {
		return err
	}
	if err = e.step("gitops-menu-smoke", func() error { return e.smoke(ctx) }); err != nil {
		return err
	}
	return e.step("gitops-order-data", func() error { return e.ordersVerifyData(ctx) })
}

func (e Engine) gitopsHealth(ctx context.Context) error {
	for attempt := 0; attempt < 60; attempt++ {
		ready := true
		for _, profile := range []string{"core", "orders"} {
			raw, err := e.Client.Remote(ctx, "kubectl", "-n", "argocd", "get", "application", "coffeeshop-lab-"+profile, "-o", "json")
			if err != nil {
				return err
			}
			var app struct {
				Status struct {
					Sync           struct{ Status, Revision string }
					Health         struct{ Status string }
					Conditions     []struct{ Type, Message string }
					OperationState struct{ Phase, Message string }
				}
			}
			if err = json.Unmarshal([]byte(raw), &app); err != nil {
				return err
			}
			for _, condition := range app.Status.Conditions {
				if strings.HasSuffix(condition.Type, "Error") {
					return fmt.Errorf("Argo %s %s: %s", profile, condition.Type, condition.Message)
				}
			}
			if app.Status.OperationState.Phase == "Failed" || app.Status.OperationState.Phase == "Error" {
				return fmt.Errorf("Argo %s sync failed: %s", profile, app.Status.OperationState.Message)
			}
			if app.Status.Sync.Status != "Synced" || app.Status.Sync.Revision != e.Config.Revision || app.Status.Health.Status != "Healthy" {
				ready = false
			}
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("Argo applications did not reach Synced/Healthy at requested revision")
}
