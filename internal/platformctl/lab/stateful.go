package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const dataNamespace = "coffeeshop-lab-data"
const statefulDir = "infrastructure/iximiuz/stateful"
const rabbitOperator = "infrastructure/k8s/environments/dev/gitops/addons/rabbitmq-operator/cluster-operator.yaml"

type statefulArtifacts struct {
	Storage, Postgres, Rabbit, Checker, Operator []byte
	Chart, ChartRepo                             string
	CertChart, CertRepo                          string
}

// Versions/images remain owned by the existing DEV contracts. Only runtime
// artifacts are assembled here; no AWS values, backup credentials or state.
func loadStateful(root string) (statefulArtifacts, error) {
	var a statefulArtifacts
	var err error
	for file, dest := range map[string]*[]byte{
		statefulDir + "/storage.yaml": &a.Storage, statefulDir + "/postgres.yaml": &a.Postgres,
		statefulDir + "/rabbitmq.yaml": &a.Rabbit, statefulDir + "/checker.yaml": &a.Checker,
		rabbitOperator: &a.Operator,
	} {
		*dest, err = os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return a, err
		}
	}
	// Explicit tags are necessary for camelCase fields in Argo's API.
	var appDoc struct {
		Spec struct {
			Source struct {
				Version string `yaml:"targetRevision"`
				Repo    string `yaml:"repoURL"`
			} `yaml:"source"`
		} `yaml:"spec"`
	}
	if err = readYAML(root, "infrastructure/k8s/environments/dev/gitops/applications/cloudnativepg.yaml", &appDoc); err != nil {
		return a, err
	}
	a.Chart, a.ChartRepo = appDoc.Spec.Source.Version, appDoc.Spec.Source.Repo
	if err = readYAML(root, "infrastructure/k8s/environments/dev/gitops/applications/cert-manager.yaml", &appDoc); err != nil {
		return a, err
	}
	a.CertRepo = appDoc.Spec.Source.Repo
	var versions struct {
		CNPG        string `yaml:"cloudnativepg_chart_version"`
		CertManager string `yaml:"cert_manager_chart_version"`
	}
	if err = readYAML(root, "infrastructure/ansible/playbooks/group_vars/all/versions.yml", &versions); err != nil {
		return a, err
	}
	a.Chart, a.CertChart = versions.CNPG, versions.CertManager
	if a.Chart == "" || a.ChartRepo != "https://cloudnative-pg.github.io/charts" {
		return a, fmt.Errorf("invalid CNPG chart contract")
	}
	if a.CertChart == "" || a.CertRepo != "https://charts.jetstack.io" {
		return a, fmt.Errorf("invalid cert-manager contract")
	}
	var pg struct {
		ImageName string `yaml:"imageName"`
	}
	if err = readYAML(root, "infrastructure/k8s/environments/dev/gitops/apps/coffeeshop-postgres/values.yaml", &pg); err != nil {
		return a, err
	}
	if !strings.Contains(pg.ImageName, "@sha256:") {
		return a, fmt.Errorf("PostgreSQL image must stay digest pinned")
	}
	var doc map[string]any
	if err = yaml.Unmarshal(a.Postgres, &doc); err != nil {
		return a, err
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok || doc["kind"] != "Cluster" {
		return a, fmt.Errorf("invalid stateful PostgreSQL manifest")
	}
	spec["imageName"] = pg.ImageName
	a.Postgres, err = yaml.Marshal(doc)
	if err != nil {
		return a, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "infrastructure/iximiuz/smoke-client.yaml"))
	if err != nil {
		return a, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var ns map[string]any
	if err = dec.Decode(&ns); err != nil {
		return a, err
	}
	var smoke struct {
		Spec struct {
			Containers []struct {
				Image string `yaml:"image"`
			} `yaml:"containers"`
		} `yaml:"spec"`
	}
	if err = dec.Decode(&smoke); err != nil {
		return a, err
	}
	if len(smoke.Spec.Containers) != 1 || smoke.Spec.Containers[0].Image == "" {
		return a, fmt.Errorf("invalid core checker image contract")
	}
	image := smoke.Spec.Containers[0].Image
	doc = nil
	if err = yaml.Unmarshal(a.Checker, &doc); err != nil {
		return a, err
	}
	spec, ok = doc["spec"].(map[string]any)
	if !ok || doc["kind"] != "Pod" {
		return a, fmt.Errorf("invalid stateful checker manifest")
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 {
		return a, fmt.Errorf("one checker container required")
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		return a, fmt.Errorf("invalid checker container")
	}
	container["image"] = image
	a.Checker, err = yaml.Marshal(doc)
	if err != nil {
		return a, err
	}
	for _, data := range [][]byte{a.Storage, a.Postgres, a.Rabbit, a.Checker} {
		if err = validateStatefulScope(data); err != nil {
			return a, err
		}
	}
	return a, err
}

func validateStatefulScope(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc struct {
			Kind          string                           `yaml:"kind"`
			Metadata      struct{ Name, Namespace string } `yaml:"metadata"`
			ReclaimPolicy string                           `yaml:"reclaimPolicy"`
		}
		if err := dec.Decode(&doc); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch doc.Kind {
		case "Namespace":
			if doc.Metadata.Name != dataNamespace {
				return fmt.Errorf("stateful namespace outside lab")
			}
		case "StorageClass":
			if doc.Metadata.Name != "coffeeshop-lab-local" || doc.ReclaimPolicy != "Retain" {
				return fmt.Errorf("stateful storage must retain lab PVs")
			}
		case "Cluster", "RabbitmqCluster", "Pod", "PodDisruptionBudget":
			if doc.Metadata.Namespace != dataNamespace {
				return fmt.Errorf("%s/%s outside stateful lab namespace", doc.Kind, doc.Metadata.Name)
			}
		default:
			return fmt.Errorf("unexpected stateful resource kind %s", doc.Kind)
		}
	}
}

func (e Engine) stateful(ctx context.Context) error {
	e.Client.RemoteTimeout = 12 * time.Minute
	a, err := loadStateful(e.Config.Root)
	if err != nil {
		return err
	}
	if err = e.step("stateful-capacity", func() error { return e.statefulCapacity(ctx) }); err != nil {
		return err
	}
	// A completed install is verified, not implicitly upgraded from changed DEV contracts.
	existing, err := e.statefulPresent(ctx)
	if err != nil {
		return err
	}
	if existing {
		raw, err := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "get", "configmap", "stateful-installed", "--ignore-not-found", "-o", "name")
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) != "" {
			if err = e.statefulReady(ctx); err != nil {
				return err
			}
			return e.statefulVerifyData(ctx)
		}
	}
	apply := func(data []byte) error {
		_, err := e.Client.RemoteInput(ctx, bytes.NewReader(data), "kubectl", "apply", "--server-side", "--field-manager=platformctl-lab", "-f", "-")
		return err
	}
	if err = e.step("stateful-storage", func() error { return apply(a.Storage) }); err != nil {
		return err
	}
	if err = e.step("stateful-operators", func() error {
		if _, err := e.Client.Remote(ctx, "helm", "upgrade", "--install", "cert-manager", "cert-manager", "--repo", a.CertRepo,
			"--namespace", "cert-manager", "--create-namespace", "--version", a.CertChart, "--set", "crds.enabled=true",
			"--set", "resources.requests.cpu=20m", "--set", "resources.requests.memory=64Mi", "--set", "resources.limits.memory=192Mi",
			"--set", "webhook.resources.requests.cpu=10m", "--set", "webhook.resources.requests.memory=32Mi", "--set", "webhook.resources.limits.memory=128Mi",
			"--set", "cainjector.resources.requests.cpu=20m", "--set", "cainjector.resources.requests.memory=64Mi", "--set", "cainjector.resources.limits.memory=192Mi",
			"--wait", "--timeout", "8m"); err != nil {
			return err
		}
		if _, err := e.Client.Remote(ctx, "helm", "upgrade", "--install", "cnpg", "cloudnative-pg", "--repo", a.ChartRepo,
			"--namespace", "cnpg-system", "--create-namespace", "--version", a.Chart,
			"--set", "resources.requests.cpu=100m", "--set", "resources.requests.memory=128Mi",
			"--set", "resources.limits.cpu=500m", "--set", "resources.limits.memory=256Mi", "--wait", "--timeout", "8m"); err != nil {
			return err
		}
		if err := apply(a.Operator); err != nil {
			return err
		}
		_, err := e.Client.Remote(ctx, "kubectl", "-n", "rabbitmq-system", "rollout", "status", "deployment/rabbitmq-cluster-operator", "--timeout=180s")
		return err
	}); err != nil {
		return err
	}
	if err = e.step("stateful-clusters", func() error {
		if err := apply(a.Postgres); err != nil {
			return err
		}
		if err := apply(a.Rabbit); err != nil {
			return err
		}
		return apply(a.Checker)
	}); err != nil {
		return err
	}
	if err = e.statefulReady(ctx); err != nil {
		return err
	}
	if err = e.statefulSeed(ctx); err != nil {
		return err
	}
	if err = e.statefulVerifyData(ctx); err != nil {
		return err
	}
	// Only metadata; data is never reset by setup or resume.
	return apply([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stateful-installed\n  namespace: " + dataNamespace + "\ndata:\n  profile: local-disk-v1\n"))
}

func (e Engine) statefulPresent(ctx context.Context) (bool, error) {
	raw, err := e.Client.Remote(ctx, "kubectl", "get", "namespace", dataNamespace, "--ignore-not-found", "-o", "json")
	if err != nil || strings.TrimSpace(raw) == "" {
		return false, err
	}
	var ns struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err = json.Unmarshal([]byte(raw), &ns); err != nil {
		return false, err
	}
	if ns.Metadata.Name != dataNamespace || ns.Metadata.Labels["app.kubernetes.io/managed-by"] != "platformctl-lab" {
		return false, fmt.Errorf("stateful namespace exists without lab ownership; refusing adoption")
	}
	return true, nil
}

func (e Engine) statefulCapacity(ctx context.Context) error {
	raw, err := e.Client.Remote(ctx, "kubectl", "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
				Taints        []struct {
					Effect string `json:"effect"`
				} `json:"taints"`
			} `json:"spec"`
			Status struct {
				Allocatable map[string]string `json:"allocatable"`
			} `json:"status"`
		} `json:"items"`
	}
	if err = json.Unmarshal([]byte(raw), &nodes); err != nil {
		return err
	}
	if len(nodes.Items) != 3 {
		return fmt.Errorf("stateful requires exactly three lab nodes")
	}
	for _, n := range nodes.Items {
		name := n.Metadata.Name
		// This pinned K3s lab reports Ki. Fail closed for unknown unit formats.
		memory := n.Status.Allocatable["memory"]
		memoryKi, parseErr := strconv.ParseInt(strings.TrimSuffix(memory, "Ki"), 10, 64)
		if n.Spec.Unschedulable || !strings.HasSuffix(memory, "Ki") || parseErr != nil || memoryKi < 3500*1024 {
			return fmt.Errorf("node %s lacks stateful capacity or has unknown memory quantity", name)
		}
		for _, taint := range n.Spec.Taints {
			if taint.Effect == "NoSchedule" || taint.Effect == "NoExecute" {
				return fmt.Errorf("node %s has blocking taint", name)
			}
		}
		if name != "cplane-01" && name != "node-01" && name != "node-02" {
			return fmt.Errorf("unexpected lab node %s", name)
		}
		disk, err := e.Client.Remote(ctx, "ssh", "-o", "StrictHostKeyChecking=accept-new", name, "df", "-Pk", "/")
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSpace(disk), "\n")
		fields := strings.Fields(lines[len(lines)-1])
		var available int64
		if len(fields) < 6 {
			return fmt.Errorf("invalid disk result on %s", name)
		}
		if _, err = fmt.Sscan(fields[3], &available); err != nil || available < 4*1024*1024 {
			return fmt.Errorf("node %s needs at least 4 GiB disk headroom", name)
		}
	}
	return nil
}
