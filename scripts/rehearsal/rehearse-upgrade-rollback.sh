#!/usr/bin/env bash
set -euo pipefail

GUARD_NAMESPACE="platform-ownership-guard-system"
GUARD_DEPLOYMENT="platform-ownership-guard-controller-manager"
CRD_NAME="ownershipaudits.guard.platform.t2dzung.github.io"
DRY_RUN=true
LIVE_CONFIRM=false
CHECKPOINT=""
EXPECTED_DIGEST=""
EVIDENCE_DIR=""

usage() {
  cat <<EOF
Usage: $0 [--dry-run] | --live --confirm-destructive --checkpoint CHECKPOINT \\
          --expected-digest sha256:<64-hex> --evidence-dir DIR

CHECKPOINT: baseline, upgrade, rollback, or restore.
This helper never changes Git or the cluster. Make the exact-digest GitOps change,
wait for Argo CD, then run the matching checkpoint to record evidence.
EOF
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    --live) DRY_RUN=false; shift ;;
    --confirm-destructive) LIVE_CONFIRM=true; shift ;;
    --checkpoint) CHECKPOINT="${2:-}"; shift 2 ;;
    --expected-digest) EXPECTED_DIGEST="${2:-}"; shift 2 ;;
    --evidence-dir) EVIDENCE_DIR="${2:-}"; shift 2 ;;
    --help) usage 0 ;;
    *) echo "Unknown or incomplete option: $1" >&2; usage 2 ;;
  esac
done

if [[ "${DRY_RUN}" == true ]]; then
  cat <<EOF
Phase 6.8 upgrade/rollback rehearsal (DRY-RUN)
1. baseline: record N-1 digest, CRD/CR state, and GitOps health.
2. Commit digest N to the branch tracked by Argo CD (currently feat/k3s-ha).
3. upgrade: assert digest N and compatibility with the existing CR.
4. Revert the Git commit and wait for Argo CD to converge to N-1.
5. rollback: assert digest N-1 and compatibility; optionally restore accepted N.
Use one persistent evidence directory for every checkpoint.
EOF
  exit 0
fi

[[ "${LIVE_CONFIRM}" == true ]] || { echo "Live verification requires --confirm-destructive." >&2; exit 1; }
[[ "${CHECKPOINT}" =~ ^(baseline|upgrade|rollback|restore)$ ]] || { echo "Invalid --checkpoint." >&2; exit 1; }
[[ "${EXPECTED_DIGEST}" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "Invalid --expected-digest." >&2; exit 1; }
[[ -n "${EVIDENCE_DIR}" ]] || { echo "Live verification requires --evidence-dir." >&2; exit 1; }
for command_name in kubectl jq; do
  command -v "${command_name}" >/dev/null || { echo "Missing ${command_name}." >&2; exit 1; }
done

mkdir -p "${EVIDENCE_DIR}"
LIVE_IMAGE=$(kubectl get deployment -n "${GUARD_NAMESPACE}" "${GUARD_DEPLOYMENT}" -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')
[[ "${LIVE_IMAGE}" == *@"${EXPECTED_DIGEST}" ]] || { echo "Live image ${LIVE_IMAGE} does not match ${EXPECTED_DIGEST}." >&2; exit 1; }

kubectl rollout status deployment/"${GUARD_DEPLOYMENT}" -n "${GUARD_NAMESPACE}" --timeout=180s
kubectl get crd "${CRD_NAME}" -o yaml > "${EVIDENCE_DIR}/${CHECKPOINT}-crd.yaml"
kubectl get ownershipaudits -A -o yaml > "${EVIDENCE_DIR}/${CHECKPOINT}-ownershipaudits.yaml"
STORED=$(kubectl get crd "${CRD_NAME}" -o json | jq -c '.status.storedVersions')
[[ "${STORED}" == '["v1alpha1"]' ]] || { echo "Unexpected storedVersions: ${STORED}" >&2; exit 1; }
kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership -o json | \
  jq -e '.status.conditions[] | select(.type == "Ready" and .status == "True")' >/dev/null
kubectl get applications.argoproj.io -n argocd platform-ownership-guard -o json | \
  jq -e '.status.sync.status == "Synced" and .status.health.status == "Healthy"' >/dev/null
printf '%s\n' "${EXPECTED_DIGEST}" > "${EVIDENCE_DIR}/${CHECKPOINT}-digest.txt"
printf 'Checkpoint %s passed for %s\n' "${CHECKPOINT}" "${EXPECTED_DIGEST}"
