package prod

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyEndpointContains(t *testing.T) {
	t.Parallel()
	data := []byte(`{"items":[{"spec":{
		"policyRef":{"name":"resilience-db-network-denied"},
		"podIsolation":["Egress"],
		"podSelectorEndpoints":[{"name":"counter-abc"}]
	}}]}`)
	require.True(t, policyEndpointContains(data, "resilience-db-network-denied", "counter-abc"))
	require.False(t, policyEndpointContains(data, "other", "counter-abc"))
}

func TestReadyPeerCount(t *testing.T) {
	t.Parallel()
	data := []byte(`{"items":[
		{"metadata":{"name":"rabbit-0"},"status":{"conditions":[{"type":"Ready","status":"False"}]}},
		{"metadata":{"name":"rabbit-1"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
		{"metadata":{"name":"rabbit-2"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}
	]}`)
	require.Equal(t, 2, readyPeerCount(data, "rabbit-0"))
}
