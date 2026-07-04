#!/usr/bin/env bash
set -Eeuo pipefail

# shellcheck source=scripts/activate-env.sh
source "$(dirname "$0")/activate-env.sh"

export KUBECONFIG="${HOME}/.kube/coffeeshop-dev.yaml"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${HOME}/coffeeshop-evidence/phase2"
mkdir -p "${EVIDENCE_DIR}"

cd "${PROJECT_ROOT}"

require_runtime_secret_manifest() {
  if [[ ! -f infrastructure/k8s/apps/configmap-secrets.yaml ]]; then
    echo "Missing infrastructure/k8s/apps/configmap-secrets.yaml." >&2
    echo "Create it from configmap-secrets.yaml.example with real base64 secrets before verification." >&2
    exit 1
  fi
}

record_nonfatal_http() {
  local title="$1"
  local url="$2"

  echo
  echo "--- ${title} ---"
  curl -s -i "${url}" | head -n 25 || true
}

cleanup_network_policy_pod() {
  kubectl delete pod np-deny-test -n coffeeshop --ignore-not-found=true --force --grace-period=0 >/dev/null 2>&1 || true
}

require_runtime_secret_manifest

echo "Applying Kubernetes manifests..."
kubectl apply -f infrastructure/k8s/apps/
kubectl apply -f infrastructure/k8s/gateway/
kubectl apply -f infrastructure/k8s/policies/

echo "Waiting for Phase 2 smoke deployment..."
kubectl wait --for=condition=available deployment/gateway-smoke -n coffeeshop --timeout=180s

echo "Gathering Cilium status..."
{
  echo "--- Cilium Pods ---"
  kubectl -n kube-system get pods -o wide | grep cilium
  echo
  echo "--- Cilium DaemonSet ---"
  kubectl -n kube-system rollout status ds/cilium --timeout=180s
  echo
  echo "--- Cilium Operator ---"
  kubectl -n kube-system rollout status deploy/cilium-operator --timeout=180s
} > "${EVIDENCE_DIR}/cilium_status.txt"

if command -v cilium >/dev/null 2>&1; then
  cilium status --wait >> "${EVIDENCE_DIR}/cilium_status.txt"
else
  echo "cilium CLI not found on control host; kubectl rollout checks above are the fallback." >> "${EVIDENCE_DIR}/cilium_status.txt"
fi

echo "Gathering Gateway status..."
{
  echo "--- Gateway Status ---"
  kubectl get gateway -A
  echo
  echo "--- Gateway Describe ---"
  kubectl describe gateway -n coffeeshop coffeeshop-gateway
  echo
  echo "--- HTTPRoute Status ---"
  kubectl get httproute -n coffeeshop
  echo
  echo "--- HTTPRoute Describe ---"
  kubectl describe httproute -n coffeeshop
} > "${EVIDENCE_DIR}/gateway_status.txt"

echo "Testing HTTP endpoints..."
HAPROXY_EIP="$(terraform -chdir=infrastructure/terraform/envs/dev output -raw active_api_endpoint)"
{
  echo "HAProxy EIP: ${HAPROXY_EIP}"
  echo
  echo "--- curl /healthz (required smoke path) ---"
  curl --fail-with-body -s -i "http://${HAPROXY_EIP}/healthz"
  record_nonfatal_http "curl / (Web; may be 503 until app image exists)" "http://${HAPROXY_EIP}/"
  record_nonfatal_http "curl /api/... (Proxy; may be 503 until app image exists)" "http://${HAPROXY_EIP}/api/v1/api/item-types"
} > "${EVIDENCE_DIR}/http_curl_results.txt"

echo "Gathering HA add-on status..."
{
  echo "--- CoreDNS / metrics-server deployments ---"
  kubectl -n kube-system get deploy coredns metrics-server -o wide
  echo
  echo "--- CoreDNS pods ---"
  kubectl -n kube-system get pods -l k8s-app=kube-dns -o wide
  echo
  echo "--- metrics-server pods ---"
  kubectl -n kube-system get pods -l k8s-app=metrics-server -o wide
  echo
  echo "--- PDBs ---"
  kubectl -n kube-system get pdb coredns-pdb metrics-server-pdb -o wide
} > "${EVIDENCE_DIR}/addon_ha_failure_test.txt"

echo "Testing NetworkPolicy deny path..."
trap cleanup_network_policy_pod EXIT
cleanup_network_policy_pod
{
  echo "--- Testing denied connection from unauthorized pod to postgres:5432 ---"
  kubectl run np-deny-test --image=curlimages/curl --labels="app=np-deny-test" -n coffeeshop --restart=Never -- sleep 120
  kubectl wait --for=condition=Ready pod/np-deny-test -n coffeeshop --timeout=60s
  echo "Expected: timeout/deny because app=np-deny-test has no allow egress to postgres."
  if kubectl exec np-deny-test -n coffeeshop -- curl -m 3 -sS telnet://postgres:5432; then
    echo "UNEXPECTED: unauthorized pod reached postgres"
    exit 1
  fi
  echo "PASS: unauthorized pod could not reach postgres:5432"
} > "${EVIDENCE_DIR}/network_policy_results.txt"
cleanup_network_policy_pod
trap - EXIT

echo "Evidence generation complete. Saved in ${EVIDENCE_DIR}:"
ls -la "${EVIDENCE_DIR}"
