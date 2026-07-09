#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"

WAIT_TIMEOUT="${WAIT_TIMEOUT:-300s}"
VERIFY_GITOPS="${VERIFY_GITOPS:-true}"
VERIFY_HTTP="${VERIFY_HTTP:-true}"
EVIDENCE_DIR="${EVIDENCE_DIR:-${HOME}/coffeeshop-evidence/dev-runtime/$(date -u +%Y%m%dT%H%M%SZ)}"

mkdir -p "${EVIDENCE_DIR}"
cd "${PROJECT_ROOT}"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

require_command kubectl

if [[ ! -f "${KUBECONFIG}" ]]; then
  echo "KUBECONFIG does not exist: ${KUBECONFIG}" >&2
  exit 1
fi

failures=0

record_failure() {
  local message="$1"
  echo "FAIL: ${message}" >&2
  failures=$((failures + 1))
}

namespace_exists() {
  kubectl get namespace "$1" >/dev/null 2>&1
}

check_namespace_pods() {
  local namespace="$1"
  local pod_lines not_ready

  if ! namespace_exists "${namespace}"; then
    echo "Namespace ${namespace} does not exist; skipping."
    return 0
  fi

  kubectl get pods -n "${namespace}" -o wide > "${EVIDENCE_DIR}/pods-${namespace}.txt"
  pod_lines="$(kubectl get pods -n "${namespace}" --no-headers 2>/dev/null || true)"

  if [[ -z "${pod_lines}" || "${pod_lines}" == No\ resources\ found* ]]; then
    echo "Namespace ${namespace} has no pods."
    return 0
  fi

  not_ready="$(awk '
    {
      split($2, ready, "/")
      if ($3 != "Running" && $3 != "Succeeded" && $3 != "Completed") {
        print
        next
      }
      if ($3 != "Succeeded" && $3 != "Completed" && ready[1] != ready[2]) {
        print
      }
    }
  ' <<< "${pod_lines}")"

  if [[ -n "${not_ready}" ]]; then
    printf '%s\n' "${not_ready}" > "${EVIDENCE_DIR}/not-ready-${namespace}.txt"
    record_failure "namespace ${namespace} has pods not Running/Succeeded or not fully Ready"
  fi
}

echo "Writing cluster snapshot to ${EVIDENCE_DIR}"
kubectl cluster-info > "${EVIDENCE_DIR}/cluster-info.txt" || true
kubectl get nodes -o wide > "${EVIDENCE_DIR}/nodes.txt"
kubectl get namespaces > "${EVIDENCE_DIR}/namespaces.txt"
kubectl get events -A --sort-by=.lastTimestamp > "${EVIDENCE_DIR}/events-all.txt" || true

if ! kubectl wait --for=condition=Ready nodes --all --timeout="${WAIT_TIMEOUT}"; then
  record_failure "not all nodes became Ready within ${WAIT_TIMEOUT}"
fi

if [[ "${VERIFY_GITOPS}" == "true" ]]; then
  if ! "${SCRIPT_DIR}/verify-gitops-health.sh"; then
    record_failure "GitOps health verification failed"
  fi
fi

for namespace in kube-system argocd coffeeshop cnpg-system longhorn-system monitoring actions-runner-system; do
  check_namespace_pods "${namespace}"
done

if kubectl get crd clusters.postgresql.cnpg.io >/dev/null 2>&1; then
  if kubectl get cluster.postgresql.cnpg.io -n coffeeshop coffeeshop-postgres >/dev/null 2>&1; then
    kubectl get cluster.postgresql.cnpg.io -n coffeeshop coffeeshop-postgres -o yaml \
      > "${EVIDENCE_DIR}/cnpg-coffeeshop-postgres.yaml"
    kubectl get pods -n coffeeshop -l cnpg.io/cluster=coffeeshop-postgres -o wide \
      > "${EVIDENCE_DIR}/cnpg-pods.txt" || true
  fi
fi

if [[ "${VERIFY_HTTP}" == "true" ]]; then
  if command -v terraform >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
    active_endpoint="$(terraform -chdir=infrastructure/terraform/envs/dev output -raw active_api_endpoint 2>/dev/null || true)"
    if [[ -n "${active_endpoint}" ]]; then
      if ! curl --fail-with-body --max-time 15 -sS -i "http://${active_endpoint}/healthz" \
          > "${EVIDENCE_DIR}/http-healthz.txt"; then
        record_failure "HTTP /healthz smoke failed for ${active_endpoint}"
      fi
    else
      echo "Terraform active_api_endpoint output is empty; skipping HTTP smoke." \
        > "${EVIDENCE_DIR}/http-healthz.txt"
    fi
  else
    echo "terraform or curl not found; skipping HTTP smoke." > "${EVIDENCE_DIR}/http-healthz.txt"
  fi
fi

if ((failures > 0)); then
  echo "Dev runtime verification failed with ${failures} failure(s)." >&2
  echo "Evidence saved in ${EVIDENCE_DIR}" >&2
  exit 1
fi

echo "Dev runtime verification passed."
echo "Evidence saved in ${EVIDENCE_DIR}"
