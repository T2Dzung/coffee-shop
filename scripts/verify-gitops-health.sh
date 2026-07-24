#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"

ARGOCD_NAMESPACE="${ARGOCD_NAMESPACE:-argocd}"
WAIT_SECONDS="${WAIT_SECONDS:-300}"
POLL_SECONDS="${POLL_SECONDS:-10}"
EVIDENCE_DIR="${EVIDENCE_DIR:-${HOME}/coffeeshop-evidence/gitops-health/$(date -u +%Y%m%dT%H%M%SZ)}"
FAIL_ON_CONDITIONS="${FAIL_ON_CONDITIONS:-true}"

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

if (($# > 0)); then
  apps=("$@")
else
  mapfile -t apps < <(
    kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
  )
fi

if ((${#apps[@]} == 0)); then
  echo "No ArgoCD Applications found in namespace ${ARGOCD_NAMESPACE}." >&2
  exit 1
fi

write_app_snapshot() {
  kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" -o wide \
    > "${EVIDENCE_DIR}/argocd-apps.txt" || true
  kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" -o yaml \
    > "${EVIDENCE_DIR}/argocd-apps.yaml" || true
  kubectl get pods -n "${ARGOCD_NAMESPACE}" -o wide \
    > "${EVIDENCE_DIR}/argocd-pods.txt" || true
  kubectl get events -n "${ARGOCD_NAMESPACE}" --sort-by=.lastTimestamp \
    > "${EVIDENCE_DIR}/argocd-events.txt" || true
}

check_once() {
  local app sync_status health_status condition_types condition_messages failures
  failures=0
  : > "${EVIDENCE_DIR}/argocd-app-status.tsv"
  printf 'APP\tSYNC\tHEALTH\tCONDITIONS\n' >> "${EVIDENCE_DIR}/argocd-app-status.tsv"

  for app in "${apps[@]}"; do
    if ! kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" >/dev/null 2>&1; then
      printf '%s\t%s\t%s\t%s\n' "${app}" "Missing" "Missing" "Application not found" \
        >> "${EVIDENCE_DIR}/argocd-app-status.tsv"
      failures=$((failures + 1))
      continue
    fi

    sync_status="$(kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" \
      -o jsonpath='{.status.sync.status}')"
    health_status="$(kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" \
      -o jsonpath='{.status.health.status}')"
    condition_types="$(kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" \
      -o jsonpath='{range .status.conditions[*]}{.type}{","}{end}')"
    condition_messages="$(kubectl get applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" \
      -o jsonpath='{range .status.conditions[*]}{.type}{": "}{.message}{"\n"}{end}')"

    printf '%s\t%s\t%s\t%s\n' "${app}" "${sync_status:-Unknown}" "${health_status:-Unknown}" "${condition_types:-None}" \
      >> "${EVIDENCE_DIR}/argocd-app-status.tsv"

    if [[ "${sync_status}" != "Synced" || "${health_status}" != "Healthy" ]]; then
      failures=$((failures + 1))
      kubectl describe applications.argoproj.io -n "${ARGOCD_NAMESPACE}" "${app}" \
        > "${EVIDENCE_DIR}/describe-${app}.txt" || true
    fi

    if [[ "${FAIL_ON_CONDITIONS}" == "true" && -n "${condition_types}" ]]; then
      failures=$((failures + 1))
      printf '%s\n' "${condition_messages}" > "${EVIDENCE_DIR}/conditions-${app}.txt"
    fi
  done

  return "${failures}"
}

write_app_snapshot

deadline=$((SECONDS + WAIT_SECONDS))
while true; do
  if check_once; then
    echo "All requested ArgoCD apps are Synced/Healthy with no blocking conditions."
    column -t -s $'\t' "${EVIDENCE_DIR}/argocd-app-status.tsv" || cat "${EVIDENCE_DIR}/argocd-app-status.tsv"
    echo "Evidence saved in ${EVIDENCE_DIR}"
    exit 0
  fi

  if ((SECONDS >= deadline)); then
    write_app_snapshot
    echo "ArgoCD health verification failed after ${WAIT_SECONDS}s." >&2
    column -t -s $'\t' "${EVIDENCE_DIR}/argocd-app-status.tsv" >&2 || cat "${EVIDENCE_DIR}/argocd-app-status.tsv" >&2
    echo "Evidence saved in ${EVIDENCE_DIR}" >&2
    exit 1
  fi

  sleep "${POLL_SECONDS}"
done
