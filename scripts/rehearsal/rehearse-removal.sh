#!/usr/bin/env bash
set -euo pipefail

GUARD_NAMESPACE="platform-ownership-guard-system"
GUARD_DEPLOYMENT="platform-ownership-guard-controller-manager"
CRD_NAME="ownershipaudits.guard.platform.t2dzung.github.io"
DRY_RUN=true
LIVE_CONFIRM=false
CHECKPOINT=""
EVIDENCE_DIR=""

usage() {
  cat <<EOF
Usage: $0 [--dry-run] | --live --confirm-destructive \\
          --checkpoint pre-remove|removed|restored --evidence-dir DIR

This read-only helper never prunes or restores resources. Gate G4 still requires
explicit user approval and a Git source-of-truth commit immediately before removal.
EOF
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    --live) DRY_RUN=false; shift ;;
    --confirm-destructive) LIVE_CONFIRM=true; shift ;;
    --checkpoint) CHECKPOINT="${2:-}"; shift 2 ;;
    --evidence-dir) EVIDENCE_DIR="${2:-}"; shift 2 ;;
    --help) usage 0 ;;
    *) echo "Unknown or incomplete option: $1" >&2; usage 2 ;;
  esac
done

if [[ "${DRY_RUN}" == true ]]; then
  cat <<EOF
Phase 6.8 removal/reinstall rehearsal (DRY-RUN)
1. pre-remove: export CRD/CR state and normalized workload fingerprint.
2. After explicit G4 approval, remove the Guard bundle through Git only.
3. removed: prove Guard resources are absent and target specs are unchanged.
4. Restore Git and recreate the OwnershipAudit from Git if required.
5. restored: prove 2/2 replicas, Lease, Ready audit, and unchanged target specs.
Use one persistent --evidence-dir for all three checkpoints.
EOF
  exit 0
fi

[[ "${LIVE_CONFIRM}" == true ]] || { echo "Gate G4 verification requires --confirm-destructive." >&2; exit 1; }
[[ "${CHECKPOINT}" =~ ^(pre-remove|removed|restored)$ ]] || { echo "Invalid --checkpoint." >&2; exit 1; }
[[ -n "${EVIDENCE_DIR}" ]] || { echo "Live verification requires --evidence-dir." >&2; exit 1; }
for command_name in kubectl jq sha256sum; do
  command -v "${command_name}" >/dev/null || { echo "Missing ${command_name}." >&2; exit 1; }
done
mkdir -p "${EVIDENCE_DIR}"

fingerprint() {
  kubectl get deployments,statefulsets,replicasets -n coffeeshop -o json | \
    jq -S '[.items[] | {apiVersion: .apiVersion, kind: .kind, name: .metadata.name, spec: .spec}] | sort_by(.kind, .name)' | \
    sha256sum | awk '{print $1}'
}

CURRENT_FINGERPRINT=$(fingerprint)
if [[ "${CHECKPOINT}" == pre-remove ]]; then
  kubectl get crd "${CRD_NAME}" -o yaml > "${EVIDENCE_DIR}/pre-remove-crd.yaml"
  kubectl get ownershipaudits -A -o yaml > "${EVIDENCE_DIR}/pre-remove-ownershipaudits.yaml"
  kubectl get deployments,statefulsets,secrets -n coffeeshop -o json | \
    jq -e '[.items[] | select(((.metadata.finalizers // []) | length) > 0 or ((.metadata.ownerReferences // []) | map(.apiVersion | startswith("guard.platform.t2dzung.github.io/")) | any))] | length == 0' >/dev/null
  printf '%s\n' "${CURRENT_FINGERPRINT}" > "${EVIDENCE_DIR}/target-fingerprint.txt"
elif [[ "${CHECKPOINT}" == removed ]]; then
  [[ -f "${EVIDENCE_DIR}/target-fingerprint.txt" ]] || { echo "Missing pre-remove fingerprint." >&2; exit 1; }
  ! kubectl get deployment -n "${GUARD_NAMESPACE}" "${GUARD_DEPLOYMENT}" >/dev/null 2>&1 || { echo "Guard Deployment still exists." >&2; exit 1; }
  ! kubectl get crd "${CRD_NAME}" >/dev/null 2>&1 || { echo "Guard CRD still exists." >&2; exit 1; }
  [[ "${CURRENT_FINGERPRINT}" == "$(<"${EVIDENCE_DIR}/target-fingerprint.txt")" ]] || { echo "Target workload specs changed during removal." >&2; exit 1; }
else
  [[ -f "${EVIDENCE_DIR}/target-fingerprint.txt" ]] || { echo "Missing pre-remove fingerprint." >&2; exit 1; }
  kubectl rollout status deployment/"${GUARD_DEPLOYMENT}" -n "${GUARD_NAMESPACE}" --timeout=180s
  [[ "$(kubectl get deployment -n "${GUARD_NAMESPACE}" "${GUARD_DEPLOYMENT}" -o jsonpath='{.status.readyReplicas}')" == 2 ]]
  kubectl get lease -n "${GUARD_NAMESPACE}" platform-ownership-guard-leader -o jsonpath='{.spec.holderIdentity}' | grep -q .
  kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership -o json | \
    jq -e '.status.conditions[] | select(.type == "Ready" and .status == "True")' >/dev/null
  [[ "${CURRENT_FINGERPRINT}" == "$(<"${EVIDENCE_DIR}/target-fingerprint.txt")" ]] || { echo "Target workload specs changed after reinstall." >&2; exit 1; }
fi

printf 'Checkpoint %s passed; target fingerprint %s\n' "${CHECKPOINT}" "${CURRENT_FINGERPRINT}"
