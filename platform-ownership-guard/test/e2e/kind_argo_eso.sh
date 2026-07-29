#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-ownership-guard-v011-argo-eso}"
readonly KEEP_CLUSTER="${KEEP_KIND_CLUSTER:-false}"

for tool in kind kubectl jq; do
  command -v "${tool}" >/dev/null || {
    echo "required tool is unavailable: ${tool}" >&2
    exit 1
  }
done

cleanup() {
  if [[ "${KEEP_CLUSTER}" != "true" ]] && kind get clusters | grep -Fxq "${CLUSTER_NAME}"; then
    kind delete cluster --name "${CLUSTER_NAME}"
  fi
}
trap cleanup EXIT

# Reuse the existing deployment/RBAC harness and prove the native-only boundary
# before adding the minimal Argo Application API used by this test.
KIND_CLUSTER_NAME="${CLUSTER_NAME}" KEEP_KIND_CLUSTER=true \
  bash "${SCRIPT_DIR}/kind_no_argo.sh"

kubectl apply --server-side --field-manager=kind-e2e \
  -f "${SCRIPT_DIR}/fixtures/application-crd.yaml"
kubectl wait --for=condition=Established --timeout=60s \
  crd/applications.argoproj.io

kubectl apply --server-side --field-manager=kind-e2e \
  -f "${SCRIPT_DIR}/fixtures/argo-eso-prune-risk.yaml"

# Keep Application evidence fresh without installing or faking an Argo controller.
reconciled_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl -n argocd patch application eso-prune-app --type=merge \
  -p "{\"status\":{\"reconciledAt\":\"${reconciled_at}\"}}"

wait_for_finding_count() {
  local expected="$1"
  local transition="$2"
  local attempt current
  for attempt in $(seq 1 30); do
    current="$(kubectl -n coffeeshop get ownershipaudit eso-prune-risk \
      -o json | jq -r '.status.summary.totalFindings // 0')"
    if [[ "${current}" == "${expected}" ]]; then
      return 0
    fi
    kubectl -n coffeeshop annotate ownershipaudit eso-prune-risk \
      "guard.platform.t2dzung.github.io/eso-rescan=${transition}-${attempt}" --overwrite >/dev/null
    sleep 2
  done
  kubectl -n coffeeshop get ownershipaudit eso-prune-risk -o yaml >&2
  echo "expected ${expected} ESO prune finding(s), got ${current}" >&2
  return 1
}

wait_for_finding_count 1 risky
kubectl -n coffeeshop get ownershipaudit eso-prune-risk -o json | jq -e '
  .status.summary.totalFindings == 1 and
  .status.summary.confirmed == 1 and
  (.status.findings | length) == 1 and
  .status.findings[0].detector == "ArgoPruneRisk" and
  .status.findings[0].target.apiGroup == "external-secrets.io" and
  .status.findings[0].target.kind == "ExternalSecret" and
  .status.findings[0].target.name == "prune-candidate" and
  .status.findings[0].confidence == "Confirmed" and
  .status.findings[0].severity == "Warning"
' >/dev/null
finding_id_before="$(kubectl -n coffeeshop get ownershipaudit eso-prune-risk \
  -o jsonpath='{.status.findings[0].id}')"

kubectl -n coffeeshop annotate externalsecret prune-candidate \
  argocd.argoproj.io/sync-options='Prune=false' --overwrite
wait_for_finding_count 0 protected

kubectl -n coffeeshop annotate externalsecret prune-candidate \
  argocd.argoproj.io/sync-options-
wait_for_finding_count 1 risk-restored
finding_id_after="$(kubectl -n coffeeshop get ownershipaudit eso-prune-risk \
  -o jsonpath='{.status.findings[0].id}')"
if [[ "${finding_id_before}" != "${finding_id_after}" ]]; then
  echo "ESO prune finding ID changed across protect/restore transitions" >&2
  exit 1
fi

if kubectl -n platform-ownership-guard-system logs \
  deployment/platform-ownership-guard-controller-manager --all-pods=true --prefix \
  | grep -E 'panic|forbidden|cannot list|cannot get'; then
  echo "controller logs contain runtime/RBAC failures" >&2
  exit 1
fi

echo "PASS: ExternalSecret prune risk confirmed, Prune=false clears it, stable restore"
