package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
	"gopkg.in/yaml.v3"
)

func newTestEvidence() *evidence.Recorder { return evidence.New("stateful-test") }

func TestStatefulArtifacts(t *testing.T) {
	cfg := testConfig(t)
	a, err := loadStateful(cfg.Root)
	require.NoError(t, err)
	require.Equal(t, "0.29.0", a.Chart)
	require.Equal(t, "v1.19.4", a.CertChart, "central version contract overrides old Application placeholder")
	var pg, checker map[string]any
	require.NoError(t, yaml.Unmarshal(a.Postgres, &pg))
	require.NoError(t, yaml.Unmarshal(a.Checker, &checker))
	require.Contains(t, pg["spec"].(map[string]any)["imageName"], "@sha256:")
	require.Equal(t, 2, pg["spec"].(map[string]any)["instances"])
	require.NotContains(t, checker["spec"], "instances")
	require.Equal(t, "Pod", checker["kind"])
	require.Contains(t, checker["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"], "curlimages/curl:")
	dec := yaml.NewDecoder(bytes.NewReader(a.Storage))
	var ns, sc map[string]any
	require.NoError(t, dec.Decode(&ns))
	require.NoError(t, dec.Decode(&sc))
	require.Equal(t, dataNamespace, ns["metadata"].(map[string]any)["name"])
	require.Equal(t, "Retain", sc["reclaimPolicy"])
	require.Equal(t, "WaitForFirstConsumer", sc["volumeBindingMode"])
	require.NotContains(t, string(a.Postgres), "barman")
	require.NotContains(t, string(a.Rabbit), "externalSecret")
}

func TestStatefulScopeRefusesForeignResources(t *testing.T) {
	for _, yaml := range []string{
		"kind: Cluster\nmetadata: {name: production, namespace: coffeeshop}\n",
		"kind: StorageClass\nmetadata: {name: coffeeshop-lab-local}\nreclaimPolicy: Delete\n",
		"kind: Secret\nmetadata: {name: credentials, namespace: coffeeshop-lab-data}\n",
	} {
		require.Error(t, validateStatefulScope([]byte(yaml)))
	}
}

type statefulRunner struct {
	calls      []command.Request
	mode       string
	queueReads int
}

func (r *statefulRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.calls = append(r.calls, req)
	remote := req.Args[len(req.Args)-1]
	out := func(s string) (command.Result, error) { return command.Result{Stdout: s}, nil }
	switch {
	case strings.Contains(remote, "'get' 'nodes'"):
		items := []any{}
		for _, name := range []string{"cplane-01", "node-01", "node-02"} {
			mem := "4090744Ki"
			if r.mode == "low-memory" {
				mem = "100000Ki"
			}
			items = append(items, map[string]any{"metadata": map[string]any{"name": name}, "spec": map[string]any{}, "status": map[string]any{"allocatable": map[string]string{"memory": mem}}})
		}
		data, _ := json.Marshal(map[string]any{"items": items})
		return out(string(data))
	case strings.Contains(remote, "'df'"):
		if r.mode == "low-disk" {
			return out("Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/root 20000000 19000000 1000000 95% /\n")
		}
		return out("Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/root 20000000 4000000 16000000 20% /\n")
	case strings.Contains(remote, "'get' 'namespace'"):
		if r.mode == "foreign" {
			return out(`{"metadata":{"name":"coffeeshop-lab-data"}}`)
		}
		return out("")
	case strings.Contains(remote, "'get' 'configmap'"):
		if strings.Contains(remote, "'stateful-installed'") {
			return out("configmap/stateful-installed")
		}
		return out("")
	case strings.Contains(remote, "'helm'"):
		if r.mode == "operator-fail" {
			return command.Result{}, errors.New("helm failed")
		}
		return out("")
	case strings.Contains(remote, "'apply'") || strings.Contains(remote, "'wait'") || strings.Contains(remote, "'rollout'"):
		return out("")
	case strings.Contains(remote, "'get' 'cluster.postgresql.cnpg.io'"):
		return out(`{"status":{"currentPrimary":"coffeeshop-postgres-1","readyInstances":2}}`)
	case strings.Contains(remote, "CREATE SCHEMA"):
		return out("INSERT 0 1")
	case strings.Contains(remote, "SELECT value"):
		if r.mode == "lost-row" {
			return out("")
		}
		return out(persistenceValue + "\n")
	case strings.Contains(remote, "/nodes'"):
		return out(`[{"running":true},{"running":true},{"running":true}]`)
	case strings.Contains(remote, "'/get'") || strings.Contains(remote, fixtureQueue+"/get"):
		r.queueReads++
		if r.mode == "lost-message" {
			return out(`[]`)
		}
		if r.mode == "fresh-queue" && r.queueReads == 1 {
			return out(`[]`)
		}
		return out(`[{"payload":"` + persistenceValue + `"}]`)
	case strings.Contains(remote, "/publish'"):
		return out(`{"routed":true}`)
	case strings.Contains(remote, "'PUT'"):
		return out("")
	}
	return command.Result{}, fmt.Errorf("unexpected request: %s", remote)
}

func TestStatefulInstallBoundaries(t *testing.T) {
	for _, mode := range []string{"", "fresh-queue", "low-memory", "low-disk", "foreign", "operator-fail"} {
		t.Run(mode, func(t *testing.T) {
			r := &statefulRunner{mode: mode}
			e := Engine{Config: testConfig(t), Client: iximiuz.Client{Runner: r}}
			// Engine.Run wires output/evidence; direct tests use the same safe defaults.
			e.Output = &bytes.Buffer{}
			e.Evidence = newTestEvidence()
			err := e.stateful(context.Background())
			if mode == "" || mode == "fresh-queue" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "low-memory" || mode == "low-disk" || mode == "foreign" {
				for _, req := range r.calls {
					require.NotContains(t, req.Args[len(req.Args)-1], "'apply'")
					require.NotContains(t, req.Args[len(req.Args)-1], "'helm'")
				}
			}
		})
	}
}

type statefulResumeRunner struct {
	core fakeRunner
	data statefulRunner
}

func (r *statefulResumeRunner) Run(ctx context.Context, req command.Request) (command.Result, error) {
	remote := req.Args[len(req.Args)-1]
	if strings.Contains(remote, "'get' 'namespace'") {
		return command.Result{Stdout: `{"metadata":{"name":"coffeeshop-lab-data","labels":{"app.kubernetes.io/managed-by":"platformctl-lab"}}}`}, nil
	}
	if strings.Contains(remote, "'"+dataNamespace+"'") {
		return r.data.Run(ctx, req)
	}
	return r.core.Run(ctx, req)
}

func TestResumeDetectsStatefulAndDoesNotSeed(t *testing.T) {
	for _, mode := range []string{"", "lost-row", "lost-message"} {
		cfg := testConfig(t)
		r := &statefulResumeRunner{core: fakeRunner{cfg: cfg, mode: "running"}, data: statefulRunner{mode: mode}}
		e := Engine{Config: cfg, Client: iximiuz.Client{Runner: r}, Output: &bytes.Buffer{}, Evidence: newTestEvidence()}
		err := e.Run(context.Background(), "resume")
		if mode == "" {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		require.NotEmpty(t, r.data.calls)
		for _, req := range r.data.calls {
			remote := req.Args[len(req.Args)-1]
			require.NotContains(t, remote, "INSERT")
			require.NotContains(t, remote, "/publish")
			require.NotContains(t, remote, "'apply'")
		}
	}
}

func TestStatefulVerifyNeverReseedsLostData(t *testing.T) {
	for _, mode := range []string{"lost-row", "lost-message"} {
		r := &statefulRunner{mode: mode}
		e := Engine{Config: testConfig(t), Client: iximiuz.Client{Runner: r}, Output: &bytes.Buffer{}, Evidence: newTestEvidence()}
		require.ErrorContains(t, e.statefulVerifyData(context.Background()), "not reseeding")
		for _, req := range r.calls {
			remote := req.Args[len(req.Args)-1]
			require.NotContains(t, remote, "INSERT")
			require.NotContains(t, remote, "/publish")
		}
	}
}
