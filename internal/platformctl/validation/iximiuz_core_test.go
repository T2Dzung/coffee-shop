package validation

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"gopkg.in/yaml.v3"
)

// Render with the native owner instead of asserting source text. This is a local
// contract test: it neither contacts a cluster nor proves NetworkPolicy enforcement.
func TestIximiuzCoreRenderedIsolation(t *testing.T) {
	root := projectRootFromTest(t)
	result, err := (command.OSRunner{}).Run(context.Background(), command.Request{
		Name:    "kubectl",
		Args:    []string{"kustomize", filepath.Join(root, "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-core")},
		Timeout: 30 * time.Second,
	})
	require.NoError(t, err, result.Stderr)

	objects := map[string]map[string]any{}
	decoder := yaml.NewDecoder(strings.NewReader(result.Stdout))
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		meta := labMap(t, object["metadata"])
		kind := object["kind"].(string)
		name := meta["name"].(string)
		if kind == "Namespace" {
			require.Equal(t, "coffeeshop-lab", name)
		} else {
			require.Equal(t, "coffeeshop-lab", meta["namespace"])
		}
		key := kind + "/" + name
		require.NotContains(t, objects, key)
		objects[key] = object
	}

	// No databases, queue, Secret, PVC, Ingress, cluster role or cloud LB in core.
	require.Len(t, objects, 10)
	for _, key := range []string{
		"Namespace/coffeeshop-lab", "Deployment/product", "Deployment/proxy",
		"Service/product", "Service/proxy", "NetworkPolicy/core-default-deny",
		"NetworkPolicy/core-dns", "NetworkPolicy/core-proxy", "NetworkPolicy/core-product",
	} {
		require.Contains(t, objects, key)
	}

	for _, name := range []string{"product", "proxy"} {
		deployment := labMap(t, objects["Deployment/"+name]["spec"])
		template := labMap(t, deployment["template"])
		pod := labMap(t, template["spec"])
		require.Equal(t, false, pod["automountServiceAccountToken"])
		for _, field := range []string{"volumes", "imagePullSecrets", "hostNetwork", "hostPID", "hostIPC"} {
			require.NotContains(t, pod, field)
		}
		containers := pod["containers"].([]any)
		require.Len(t, containers, 1)
		container := labMap(t, containers[0])
		require.True(t, strings.HasPrefix(container["image"].(string), "registry.iximiuz.com/"))
		require.NotContains(t, container, "envFrom")
		security := labMap(t, container["securityContext"])
		require.Equal(t, false, security["allowPrivilegeEscalation"])
		require.Equal(t, true, security["readOnlyRootFilesystem"])

		// Kustomize must rewrite every configMapKeyRef to the generated name.
		for _, raw := range container["env"].([]any) {
			env := labMap(t, raw)
			require.False(t, strings.HasPrefix(env["name"].(string), "AWS_"))
			if from, exists := env["valueFrom"]; exists {
				ref := labMap(t, from)
				require.NotContains(t, ref, "secretKeyRef")
				cmRef := labMap(t, ref["configMapKeyRef"])
				cmKey := "ConfigMap/" + cmRef["name"].(string)
				require.Contains(t, objects, cmKey)
				data := labMap(t, objects[cmKey]["data"])
				require.Contains(t, data, cmRef["key"].(string))
				require.Equal(t, "product", data["GRPC_PRODUCT_HOST"])
				require.Equal(t, "counter.coffeeshop-lab-data.svc.cluster.local", data["GRPC_COUNTER_HOST"])
				require.Equal(t, "0", data["OTEL_TRACES_SAMPLER_ARG"])
			}
		}
		service := labMap(t, objects["Service/"+name]["spec"])
		require.NotContains(t, service, "type") // Kubernetes defaults to ClusterIP.
		require.NotContains(t, service, "externalIPs")
	}

	deny := labMap(t, objects["NetworkPolicy/core-default-deny"]["spec"])
	require.Empty(t, deny["podSelector"])
	require.ElementsMatch(t, []any{"Ingress", "Egress"}, deny["policyTypes"])
	require.Empty(t, deny["ingress"])
	require.Empty(t, deny["egress"])

	proxyPolicy := labMap(t, objects["NetworkPolicy/core-proxy"]["spec"])
	proxyEgress := proxyPolicy["egress"].([]any)
	require.Len(t, proxyEgress, 2)
	egress := labMap(t, proxyEgress[0])
	peers := egress["to"].([]any)
	require.Len(t, peers, 1)
	selector := labMap(t, labMap(t, peers[0])["podSelector"])
	require.Equal(t, map[string]any{"app": "product"}, selector["matchLabels"])
	ports := egress["ports"].([]any)
	require.Len(t, ports, 1)
	require.Equal(t, 5001, labMap(t, ports[0])["port"])
	counterRule := labMap(t, proxyEgress[1])
	counterPeer := labMap(t, counterRule["to"].([]any)[0])
	require.Equal(t, map[string]any{"app": "counter"}, labMap(t, counterPeer["podSelector"])["matchLabels"])
	require.Equal(t, map[string]any{"kubernetes.io/metadata.name": "coffeeshop-lab-data"}, labMap(t, counterPeer["namespaceSelector"])["matchLabels"])
	require.Equal(t, 5002, labMap(t, counterRule["ports"].([]any)[0])["port"])
	productPolicy := labMap(t, objects["NetworkPolicy/core-product"]["spec"])
	ingressRules := productPolicy["ingress"].([]any)
	require.Len(t, ingressRules, 1)
	ingress := labMap(t, ingressRules[0])
	sources := ingress["from"].([]any)
	require.Len(t, sources, 2)
	counterSource := labMap(t, sources[1])
	require.Equal(t, map[string]any{"app": "counter"}, labMap(t, counterSource["podSelector"])["matchLabels"])
	require.Equal(t, map[string]any{"kubernetes.io/metadata.name": "coffeeshop-lab-data"}, labMap(t, counterSource["namespaceSelector"])["matchLabels"])
	sourceSelector := labMap(t, labMap(t, sources[0])["podSelector"])
	require.Equal(t, map[string]any{"app": "proxy"}, sourceSelector["matchLabels"])
	productPorts := ingress["ports"].([]any)
	require.Len(t, productPorts, 1)
	require.Equal(t, 5001, labMap(t, productPorts[0])["port"])
}

func labMap(t *testing.T, value any) map[string]any {
	t.Helper()
	mapping, ok := value.(map[string]any)
	require.True(t, ok, "expected YAML mapping, got %T", value)
	return mapping
}
