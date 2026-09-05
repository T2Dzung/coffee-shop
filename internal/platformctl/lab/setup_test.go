package lab

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
	"gopkg.in/yaml.v3"
)

func TestLabSourceBundleAndInventory(t *testing.T) {
	cfg := testConfig(t)
	bundle, err := sourceBundle(cfg.Root)
	require.NoError(t, err)
	files := map[string]bool{}
	tr := tar.NewReader(bytes.NewReader(bundle))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		files[h.Name] = true
		require.Equal(t, byte(tar.TypeReg), h.Typeflag)
		require.Equal(t, int64(0644), h.Mode, "non-root Docker COPY must read config")
		for _, banned := range []string{"terraform/", "artifacts/", ".git/", ".aws/", "tfstate", "secrets", "internal/platformctl/"} {
			require.NotContains(t, h.Name, banned)
		}
	}
	for _, name := range []string{"cmd/proxy/main.go", "cmd/product/main.go", "cmd/counter/main.go", "cmd/migrate/main.go", "db/migrations/000001_init_counterdb.up.sql", "internal/product/app/app.go", "infrastructure/iximiuz/bootstrap.yaml", "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-orders/kustomization.yaml", "scripts/ci/build-go-service.sh", "go.mod"} {
		require.True(t, files[name], name)
	}
	data, err := (Engine{Config: cfg}).setupInventory()
	require.NoError(t, err)
	var inventory map[string]any
	require.NoError(t, yaml.Unmarshal(data, &inventory))
	hosts := inventory["all"].(map[string]any)["hosts"].(map[string]any)
	require.Len(t, hosts, 4)
	require.Equal(t, "local", hosts[cfg.Machine].(map[string]any)["ansible_connection"])
}

type setupRefusalRunner struct {
	calls []command.Request
	aws   bool
}

func (r *setupRefusalRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.calls = append(r.calls, req)
	remote := req.Args[len(req.Args)-1]
	if remote == "'env' '-0'" {
		if r.aws {
			return command.Result{Stdout: "AWS_PROFILE=production\x00"}, nil
		}
		return command.Result{}, nil
	}
	if strings.HasPrefix(remote, "'test'") {
		return command.Result{}, errors.New("target exists")
	}
	return command.Result{}, errors.New("unexpected mutation")
}

func TestLabSetupRefusesAWSAndCompletedTarget(t *testing.T) {
	for _, aws := range []bool{true, false} {
		r := &setupRefusalRunner{aws: aws}
		e := Engine{Config: testConfig(t), Client: iximiuz.Client{Runner: r}}
		require.Error(t, e.setup(context.Background()))
		for _, c := range r.calls {
			require.Nil(t, c.Stdin)
			require.NotContains(t, c.Args[len(c.Args)-1], "'mkdir'")
		}
	}
}

type createRunner struct {
	calls []command.Request
	cfg   Config
}

func (r *createRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.calls = append(r.calls, req)
	if req.Args[0] == "playground" {
		switch req.Args[1] {
		case "start":
			return command.Result{Stdout: strings.Repeat("b", 24) + "\n"}, nil
		case "status":
			data, _ := json.Marshal(iximiuz.Status{ID: strings.Repeat("b", 24), Playground: r.cfg.Playground, State: "RUNNING"})
			return command.Result{Stdout: string(data)}, nil
		default:
			return command.Result{}, nil
		}
	}
	// A deliberate fail-fast boundary after creation, before any source transfer.
	return command.Result{Stdout: "AWS_PROFILE=blocked\x00"}, nil
}

func TestLabSetupCreationUsesReturnedIDAndTimebox(t *testing.T) {
	cfg := testConfig(t)
	cfg.RunID = ""
	r := &createRunner{cfg: cfg}
	e := Engine{Config: cfg, Client: iximiuz.Client{Runner: r}}
	require.ErrorContains(t, e.Run(context.Background(), "setup"), "AWS environment")
	require.Equal(t, []string{"playground", "start", cfg.Playground, "--quiet"}, r.calls[0].Args)
	require.Equal(t, []string{"playground", "persist", strings.Repeat("b", 24)}, r.calls[2].Args)
	require.Equal(t, []string{"playground", "lifetime", strings.Repeat("b", 24), "1h0m0s"}, r.calls[3].Args)
	require.Equal(t, strings.Repeat("b", 24), r.calls[4].Args[1])
	require.Len(t, r.calls, 5)
}

type setupRunner struct {
	base  fakeRunner
	calls []command.Request
	fail  string
}

func (r *setupRunner) Run(ctx context.Context, req command.Request) (command.Result, error) {
	r.calls = append(r.calls, req)
	remote := req.Args[len(req.Args)-1]
	if r.fail != "" && strings.Contains(remote, r.fail) {
		return command.Result{}, errors.New("injected setup failure")
	}
	if req.Args[0] == "ssh" {
		for _, prefix := range []string{"'test'", "'mkdir'", "'touch'", "'tar'", "'tee'", "'sudo'", "'python3'", "'/home/laborant/coffeeshop-core/.venv/", "'docker' 'run'", "'docker' 'build'", "'kubectl' 'apply'"} {
			if strings.HasPrefix(remote, prefix) {
				return command.Result{}, nil
			}
		}
	}
	return r.base.Run(ctx, req)
}

func TestLabSetupHappyPathAndFailureBoundaries(t *testing.T) {
	for _, fail := range []string{"", ".venv/bin/ansible-playbook", "'docker' 'run'", "'kubectl' 'apply'", "'wait'"} {
		t.Run(fail, func(t *testing.T) {
			cfg := testConfig(t)
			r := &setupRunner{base: fakeRunner{cfg: cfg, mode: "running"}, fail: fail}
			e := Engine{Config: cfg, Client: iximiuz.Client{Runner: r, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
			err := e.Run(context.Background(), "setup")
			if fail == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			marked := false
			for _, req := range r.calls {
				remote := req.Args[len(req.Args)-1]
				if strings.HasPrefix(remote, "'touch'") && strings.Contains(remote, ".core-installed") {
					marked = true
				}
			}
			require.Equal(t, fail == "", marked, "success marker only after health and smoke")
		})
	}
}
