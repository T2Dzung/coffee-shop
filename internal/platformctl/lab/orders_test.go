package lab

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
)

func TestOrdersProfileContract(t *testing.T) {
	cfg := testConfig(t)
	require.ElementsMatch(t, []string{"counter", "barista", "kitchen", "migrate"}, cfg.OrderComponents)
	require.Len(t, cfg.OrderImages, 4)
	images := make([]Image, 0, len(cfg.OrderImages))
	for name := range cfg.OrderImages {
		images = append(images, Image{Name: name, NewName: name, Digest: "sha256:" + strings.Repeat("a", 64)})
	}
	manifest, err := (Engine{Config: cfg}).orderMigrationManifest(images)
	require.NoError(t, err)
	require.Contains(t, string(manifest), "registry.iximiuz.com/go-coffeeshop-migrate@sha256:")
	require.Contains(t, string(manifest), "$(PG_URI)?sslmode=require")
	require.Contains(t, string(manifest), "mountPath: /tmp")
	require.Contains(t, string(manifest), "sizeLimit: 16Mi")
	require.NotContains(t, string(manifest), "go-coffeeshop-migrate\n")
	require.NotContains(t, string(manifest), "password:")

}

type ordersAbsentRunner struct{ calls []command.Request }

func (r *ordersAbsentRunner) Run(_ context.Context, req command.Request) (command.Result, error) {
	r.calls = append(r.calls, req)
	remote := req.Args[len(req.Args)-1]
	if strings.Contains(remote, "'get' 'namespace'") {
		return command.Result{}, nil
	}
	return command.Result{}, nil
}

func TestOrdersRefusesWithoutStatefulBeforeMutation(t *testing.T) {
	r := &ordersAbsentRunner{}
	e := Engine{Config: testConfig(t), Client: iximiuz.Client{Runner: r}, Output: &bytes.Buffer{}, Evidence: newTestEvidence()}
	require.ErrorContains(t, e.orders(context.Background()), "requires the stateful profile")
	for _, req := range r.calls {
		remote := req.Args[len(req.Args)-1]
		require.NotContains(t, remote, "'apply'")
		require.NotContains(t, remote, "'docker'")
		require.NotContains(t, remote, "'tar'")
	}
}

func TestMigrationJobState(t *testing.T) {
	tests := []struct {
		raw                      string
		exists, complete, failed bool
		wantErr                  bool
	}{
		{"", false, false, false, false},
		{`{"status":{"conditions":[{"type":"Complete","status":"True"}]}}`, true, true, false, false},
		{`{"status":{"conditions":[{"type":"Failed","status":"True"}]}}`, true, false, true, false},
		{`{"status":{"conditions":[]}}`, true, false, false, false},
		{"{", true, false, false, true},
	}
	for _, tt := range tests {
		exists, complete, failed, err := migrationJobState(tt.raw)
		require.Equal(t, tt.wantErr, err != nil)
		require.Equal(t, tt.exists, exists)
		require.Equal(t, tt.complete, complete)
		require.Equal(t, tt.failed, failed)
	}
}
