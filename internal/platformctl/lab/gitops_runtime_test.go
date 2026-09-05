package lab

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
)

type argoReadRunner struct {
	response string
	calls    int
}

func (r *argoReadRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.calls++
	if !strings.Contains(req.Args[len(req.Args)-1], "'get' 'application'") {
		return command.Result{}, fmt.Errorf("unexpected mutation")
	}
	return command.Result{Stdout: r.response}, nil
}

func TestGitOpsRevisionFailsBeforeProviderAccess(t *testing.T) {
	cfg := testConfig(t)
	cfg.Revision = "lab"
	runner := &argoReadRunner{}
	err := (Engine{Config: cfg, Client: iximiuz.Client{Runner: runner}}).Run(context.Background(), "gitops")
	require.ErrorContains(t, err, "full commit SHA")
	require.Zero(t, runner.calls)
}

func TestGitOpsHealth(t *testing.T) {
	cfg := testConfig(t)
	cfg.Revision = strings.Repeat("a", 40)
	for _, tc := range []struct{ name, body, want string }{
		{"healthy", `{"status":{"sync":{"status":"Synced","revision":"` + cfg.Revision + `"},"health":{"status":"Healthy"}}}`, ""},
		{"comparison failure", `{"status":{"conditions":[{"type":"ComparisonError","message":"source unavailable"}]}}`, "source unavailable"},
		{"sync failure", `{"status":{"operationState":{"phase":"Failed","message":"denied"}}}`, "denied"},
		{"bad JSON", `{`, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &argoReadRunner{response: tc.body}
			e := Engine{Config: cfg, Client: iximiuz.Client{Runner: runner}}
			err := e.gitopsHealth(context.Background())
			if tc.want == "" {
				require.NoError(t, err)
				require.Equal(t, 2, runner.calls)
			} else {
				require.Error(t, err)
				require.Equal(t, 1, runner.calls)
			}
		})
	}
}
