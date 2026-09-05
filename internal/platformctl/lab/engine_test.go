package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
	"gopkg.in/yaml.v3"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	cfg, err := Load(filepath.Join(filepath.Dir(file), "../../.."), Options{
		RunID: strings.Repeat("a", 24), Playground: "coffeeshop-core-12345678", Binary: "labctl",
		Machine: "dev-machine", Workspace: "/home/laborant/coffeeshop-core", Lifetime: time.Hour,
	})
	require.NoError(t, err)
	return cfg
}

type fakeRunner struct {
	cfg    Config
	mode   string
	calls  []command.Request
	cancel context.CancelFunc
}

func (f *fakeRunner) Run(ctx context.Context, req command.Request) (command.Result, error) {
	if err := ctx.Err(); err != nil {
		return command.Result{}, err
	}
	f.calls = append(f.calls, req)
	result := func(v string) (command.Result, error) { return command.Result{Stdout: v}, nil }
	if req.Args[0] == "playground" {
		if req.Args[1] != "status" {
			if f.mode == req.Args[1]+"-fail" {
				return command.Result{}, errors.New("provider operation failed")
			}
			return result("")
		}
		state := "STOPPED"
		if f.mode == "running" {
			state = "RUNNING"
		}
		if f.mode == "transition" {
			state = "STOPPING"
		}
		name := f.cfg.Playground
		if f.mode == "wrong-target" {
			name = "other-lab"
		}
		data, _ := json.Marshal(iximiuz.Status{ID: f.cfg.RunID, Playground: name, State: state})
		return result(string(data))
	}
	remote := req.Args[len(req.Args)-1]
	switch {
	case strings.Contains(remote, "'get' 'namespace'"):
		return result("")
	case remote == "'env' '-0'":
		if f.mode == "aws" {
			return result("AWS_ACCESS_KEY_ID=PRIVATE-TEST-VALUE\x00")
		}
		return result("PATH=/usr/bin\x00")
	case strings.Contains(remote, "'config' 'view'"):
		server := f.cfg.APIServer
		if f.mode == "wrong-api" {
			server = "https://prod.example.com"
		}
		return result(`{"clusters":[{"cluster":{"server":"` + server + `"}}]}`)
	case strings.Contains(remote, "'ciliumnetworkpolicy'"):
		if f.mode == "fault" {
			return result("ciliumnetworkpolicy/lab-deny-product")
		}
		return result("")
	case strings.HasPrefix(remote, "'cat'"):
		var images []Image
		for name := range f.cfg.Images {
			images = append(images, Image{Name: name, NewName: name, Digest: "sha256:" + strings.Repeat("b", 64)})
		}
		if f.mode == "foreign-lock" {
			images[0].NewName = "123.dkr.ecr.example/app"
		}
		data, _ := yaml.Marshal(struct {
			Images []Image `yaml:"images"`
		}{images})
		return result(string(data))
	case strings.Contains(remote, "'image' 'inspect'"):
		if f.mode == "cache-missing" {
			return command.Result{}, errors.New("image not found")
		}
		for name := range f.cfg.Images {
			if strings.Contains(remote, name+":") {
				digest := "sha256:" + strings.Repeat("b", 64)
				if f.mode == "cache-drift" {
					digest = "sha256:" + strings.Repeat("c", 64)
				}
				data, _ := json.Marshal([]string{name + "@" + digest})
				return result(string(data))
			}
		}
	case strings.Contains(remote, "'docker' 'push'"):
		if f.cancel != nil {
			f.cancel()
		}
		if f.mode == "push-fail" {
			return command.Result{}, errors.New("registry unavailable")
		}
		return result("")
	case strings.Contains(remote, "'--head'"):
		digest := strings.Repeat("b", 64)
		if f.mode == "registry-drift" {
			digest = strings.Repeat("c", 64)
		}
		return result("HTTP/2 200\r\ndocker-content-digest: sha256:" + digest + "\r\n")
	case strings.Contains(remote, "'wait'") || strings.Contains(remote, "'rollout'"):
		if f.mode == "not-ready" {
			return command.Result{}, errors.New("timeout")
		}
		return result("")
	case strings.Contains(remote, "'get' 'service'"):
		return result(`{"spec":{"ports":[{"port":5000}]}}`)
	case strings.Contains(remote, "'get' 'gateway'"):
		reason := "AddressNotAssigned"
		if f.mode == "bad-gateway" {
			reason = "Invalid"
		}
		return result(`{"metadata":{"generation":1},"status":{"conditions":[{"type":"Accepted","status":"True","observedGeneration":1},{"type":"Programmed","status":"False","reason":"` + reason + `","observedGeneration":1}]}}`)
	case strings.Contains(remote, "'curl'"):
		if f.mode == "empty-menu" {
			return result(`{"itemTypes":[]}`)
		}
		return result(`{"itemTypes":[{"name":"COFFEE"}]}`)
	}
	return command.Result{}, errors.New("unexpected test request: " + remote)
}

func TestLabResumeSafetyAndRecovery(t *testing.T) {
	for _, mode := range []string{"", "running", "wrong-target", "transition", "restart-fail", "persist-fail", "lifetime-fail", "aws", "wrong-api", "fault", "foreign-lock", "cache-missing", "cache-drift", "push-fail", "registry-drift", "not-ready", "empty-menu", "bad-gateway"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			runner := &fakeRunner{cfg: cfg, mode: mode}
			var output bytes.Buffer
			recorder := evidence.New("test")
			engine := Engine{Config: cfg, Client: iximiuz.Client{Runner: runner, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}, Output: &output, Evidence: recorder}
			err := engine.Run(context.Background(), "resume")
			if mode == "" || mode == "running" {
				require.NoError(t, err)
				require.Contains(t, output.String(), "WARNING:")
				require.Equal(t, "passed", recorder.Snapshot().Status)
			} else {
				require.Error(t, err)
				require.Equal(t, "failed", recorder.Snapshot().Status)
			}
			joined := ""
			for _, call := range runner.calls {
				require.Equal(t, "labctl", call.Name)
				joined += strings.Join(call.Args, " ") + "\n"
			}
			for _, early := range []string{"wrong-target", "transition", "aws", "wrong-api", "fault", "foreign-lock", "cache-missing", "cache-drift"} {
				if mode == early {
					require.NotContains(t, joined, "'docker' 'push'")
				}
			}
			if mode == "wrong-target" || mode == "transition" {
				require.Len(t, runner.calls, 1)
			}
			if mode == "push-fail" || mode == "registry-drift" {
				require.NotContains(t, joined, "'wait'")
			}
			if mode == "running" {
				require.NotContains(t, joined, "playground restart")
				require.NotContains(t, joined, "playground lifetime")
			}
			if strings.HasSuffix(mode, "-fail") && mode != "push-fail" {
				require.NotContains(t, joined, "ssh ")
			}
			require.NotContains(t, output.String(), "PRIVATE-TEST-VALUE")
			require.NotContains(t, joined, " destroy ")
		})
	}
}

func TestLabStatusStopAndCancellation(t *testing.T) {
	for _, action := range []string{"status", "stop"} {
		for _, mode := range []string{"", "running"} {
			cfg := testConfig(t)
			runner := &fakeRunner{cfg: cfg, mode: mode}
			e := Engine{Config: cfg, Client: iximiuz.Client{Runner: runner, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
			require.NoError(t, e.Run(context.Background(), action))
			if action == "status" || mode == "" {
				require.Len(t, runner.calls, 1)
			} else {
				require.Len(t, runner.calls, 3)
				require.Equal(t, "persist", runner.calls[1].Args[1])
				require.Equal(t, "stop", runner.calls[2].Args[1])
			}
		}
	}
	cfg := testConfig(t)
	runner := &fakeRunner{cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := Engine{Config: cfg, Client: iximiuz.Client{Runner: runner, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
	require.ErrorIs(t, e.Run(ctx, "resume"), context.Canceled)
	require.Empty(t, runner.calls)
}

func TestLabCancellationAfterPushStopsFurtherOperations(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &fakeRunner{cfg: cfg, cancel: cancel}
	e := Engine{Config: cfg, Client: iximiuz.Client{Runner: runner, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
	require.ErrorIs(t, e.Run(ctx, "resume"), context.Canceled)
	last := runner.calls[len(runner.calls)-1]
	require.Contains(t, strings.Join(last.Args, " "), "'docker' 'push'")
}

func TestLabStopDoesNotStopIfPersistFails(t *testing.T) {
	cfg := testConfig(t)
	runner := &fakeRunner{cfg: cfg, mode: "persist-fail"}
	// This fixture needs a running initial state while failing the persist call.
	adapter := runnerWithRunningStatus{runner}
	e := Engine{Config: cfg, Client: iximiuz.Client{Runner: adapter, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
	require.Error(t, e.Run(context.Background(), "stop"))
	require.Len(t, runner.calls, 2)
	require.Equal(t, "persist", runner.calls[1].Args[1])
}

type runnerWithRunningStatus struct{ *fakeRunner }

func (r runnerWithRunningStatus) Run(ctx context.Context, req command.Request) (command.Result, error) {
	if req.Args[0] == "playground" && req.Args[1] == "status" {
		previous := r.mode
		r.mode = "running"
		result, err := r.fakeRunner.Run(ctx, req)
		r.mode = previous
		return result, err
	}
	return r.fakeRunner.Run(ctx, req)
}

func TestLabConfigRejectsUnsafeInputs(t *testing.T) {
	cfg := testConfig(t)
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../..")
	for _, mutate := range []func(*Options){
		func(o *Options) { o.RunID = "latest" }, func(o *Options) { o.Playground = "prod" },
		func(o *Options) { o.Workspace = "/home/laborant/../.aws" }, func(o *Options) { o.Workspace = "/" },
		func(o *Options) { o.Lifetime = 24 * time.Hour }, func(o *Options) { o.Machine = "node;env" },
	} {
		options := cfg.Options
		mutate(&options)
		_, err := Load(root, options)
		require.Error(t, err)
	}
}
