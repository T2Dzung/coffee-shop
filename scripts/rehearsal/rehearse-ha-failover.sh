#!/usr/bin/env bash
# ==============================================================================
# Script: rehearse-ha-failover.sh
# Purpose: Offline & Dry-Run Rehearsal Helper for Phase 6.8 HA Failover
# Safety: Default mode is DRY-RUN. Live execution requires --live --confirm-destructive
# ==============================================================================
set -euo pipefail

GUARD_NAMESPACE="platform-ownership-guard-system"
LEASE_NAME="platform-ownership-guard-leader"
DRY_RUN=true
LIVE_CONFIRM=false
CURRENT_HOLDER=""
CURRENT_POD_NAME=""
FINGERPRINT_BEFORE=""
FINGERPRINT_AFTER=""

usage() {
  cat <<EOF
Usage: $0 [options]

Options:
  --dry-run                 Run preflight, baseline recording, and print commands without mutating live pods (Default).
  --live                    Enable live execution mode (requires --confirm-destructive).
  --confirm-destructive     Explicit safety guard to allow pod deletion on live cluster.
  --help                    Show this help message.
EOF
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    --live) DRY_RUN=false; shift ;;
    --confirm-destructive) LIVE_CONFIRM=true; shift ;;
    --help) usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

if [[ "${DRY_RUN}" == false && "${LIVE_CONFIRM}" != true ]]; then
  echo "::error::Live mode requires --confirm-destructive before any cluster query." >&2
  exit 1
fi

echo "======================================================================"
echo " Phase 6.8 HA Failover Rehearsal Helper"
echo " Mode: $( [ "$DRY_RUN" = true ] && echo "DRY-RUN (Offline / Safety Guarded)" || echo "LIVE EXECUTION" )"
echo "======================================================================"

# 1. Capacity Preflight Check
preflight_capacity_check() {
  echo "[1/5] Running Capacity Preflight Check..."
  if [ "$DRY_RUN" = true ]; then
    echo "  [DRY-RUN] Capacity requirement: 2 nodes available or 1 node with sufficient headroom for replicas=2 (requests: 10m/64Mi each)."
    echo "  [DRY-RUN] Command to query live node capacity:"
    echo "    kubectl get nodes -o custom-columns=NAME:.metadata.name,CPU:.status.allocatable.cpu,MEMORY:.status.allocatable.memory"
  else
    if command -v kubectl >/dev/null 2>&1; then
      echo "  - Checking node allocatable CPU and Memory..."
      kubectl get nodes -o custom-columns=NAME:.metadata.name,CPU:.status.allocatable.cpu,MEMORY:.status.allocatable.memory
    else
      echo "::error::kubectl CLI is required for live execution!" >&2
      exit 1
    fi
  fi
}

# 2. Record Baseline & Lease Holder Identity
record_lease_baseline() {
  echo "[2/5] Recording Baseline Lease & Leader Identity..."
  if [ "$DRY_RUN" = true ]; then
    echo "  [DRY-RUN] Commands to record Lease baseline:"
    echo "    kubectl get lease -n ${GUARD_NAMESPACE} ${LEASE_NAME} -o json"
    echo "    kubectl get pods -n ${GUARD_NAMESPACE} -l app.kubernetes.io/name=platform-ownership-guard -o wide"
  else
    if ! kubectl get lease -n "${GUARD_NAMESPACE}" "${LEASE_NAME}" >/dev/null 2>&1; then
      echo "::error::Lease ${LEASE_NAME} not found in namespace ${GUARD_NAMESPACE}!" >&2
      exit 1
    fi
    CURRENT_HOLDER=$(kubectl get lease -n "${GUARD_NAMESPACE}" "${LEASE_NAME}" -o jsonpath='{.spec.holderIdentity}')
    if [[ -z "${CURRENT_HOLDER}" ]]; then
      echo "::error::Lease ${LEASE_NAME} has empty holderIdentity!" >&2
      exit 1
    fi
    # Extract Pod name from holderIdentity (holderIdentity format is <pod-name>_<uuid>)
    CURRENT_POD_NAME="${CURRENT_HOLDER%%_*}"
    RENEW_TIME=$(kubectl get lease -n "${GUARD_NAMESPACE}" "${LEASE_NAME}" -o jsonpath='{.spec.renewTime}')
    echo "  - Current Leader Raw Holder: ${CURRENT_HOLDER}"
    echo "  - Extracted Target Pod Name: ${CURRENT_POD_NAME}"
    echo "  - Lease Renew Time:          ${RENEW_TIME}"
    if ! kubectl get pod -n "${GUARD_NAMESPACE}" "${CURRENT_POD_NAME}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -Fxq True; then
      echo "::error::Lease holder does not map to a Ready Guard Pod: ${CURRENT_POD_NAME}" >&2
      exit 1
    fi
  fi
}

# Helper: Compute deterministic workload spec fingerprint (excluding status/resourceVersion/metadata noise)
compute_workload_fingerprint() {
  command -v jq >/dev/null 2>&1 || {
    echo "::error::jq is required for deterministic spec fingerprints." >&2
    return 1
  }
  kubectl get deployments,statefulsets,replicasets -n coffeeshop -o json | \
    jq -S '[.items[] | {apiVersion: .apiVersion, kind: .kind, name: .metadata.name, spec: .spec}] | sort_by(.kind, .name)' | \
    sha256sum | awk '{print $1}'
}

# 3. Fingerprint Target Workloads Before Failover
fingerprint_workloads_before() {
  echo "[3/5] Fingerprinting Target Workloads (Zero Target Mutation Verification)..."
  if [ "$DRY_RUN" = true ]; then
    echo "  [DRY-RUN] Fingerprint command filtering spec-only fields:"
    echo "    kubectl get deployments,statefulsets,replicasets -n coffeeshop -o json | jq -S '<normalized desired specs>' | sha256sum"
  else
    FINGERPRINT_BEFORE=$(compute_workload_fingerprint)
    echo "  - Workload Spec Fingerprint (Before): ${FINGERPRINT_BEFORE}"
  fi
}

# 4. Failover Simulation / Execution with Timeout & Pod Name Extraction
execute_failover() {
  echo "[4/5] Executing Leader Failover..."
  if [ "$DRY_RUN" = true ]; then
    echo "  [DRY-RUN] Safety Guard active. To execute live, run:"
    echo "    $0 --live --confirm-destructive"
    echo "  [DRY-RUN] Live execution would perform:"
    echo "    1. Extract pod name from holderIdentity (<pod-name>_<uuid> -> <pod-name>)"
    echo "    2. Delete active leader Pod: kubectl delete pod <pod-name> -n ${GUARD_NAMESPACE}"
    echo "    3. Poll Lease ${LEASE_NAME} until holderIdentity changes with 45s timeout"
    echo "    4. Record takeover duration and verify candidate SLO <= 30s"
  else
    if [ "$LIVE_CONFIRM" != true ]; then
      echo "::error::Live mode requires --confirm-destructive flag!" >&2
      exit 1
    fi

    if [[ -z "${CURRENT_POD_NAME}" ]]; then
      echo "::error::Extracted Pod name is empty! Cannot proceed with deletion." >&2
      exit 1
    fi

    echo "  - Deleting leader Pod '${CURRENT_POD_NAME}'..."
    T0=$(date +%s)
    kubectl delete pod "${CURRENT_POD_NAME}" -n "${GUARD_NAMESPACE}" --now

    echo "  - Polling for follower takeover (Timeout: 45s)..."
    MAX_WAIT=45
    ELAPSED=0
    NEW_HOLDER=""
    TAKEOVER_SUCCESS=false

    while [[ ${ELAPSED} -lt ${MAX_WAIT} ]]; do
      NEW_HOLDER=$(kubectl get lease -n "${GUARD_NAMESPACE}" "${LEASE_NAME}" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
      if [[ -n "${NEW_HOLDER}" && "${NEW_HOLDER}" != "${CURRENT_HOLDER}" ]]; then
        T1=$(date +%s)
        DURATION=$((T1 - T0))
        NEW_POD_NAME="${NEW_HOLDER%%_*}"
        echo "  - Leader takeover complete! New Leader Pod: ${NEW_POD_NAME} (Raw: ${NEW_HOLDER})"
        echo "  - Takeover Duration: ${DURATION}s (Candidate SLO <= 30s)"
        if [[ ${DURATION} -gt 30 ]]; then
          echo "::error::Takeover duration ${DURATION}s exceeded candidate SLO threshold of 30s." >&2
          exit 1
        fi
        TAKEOVER_SUCCESS=true
        break
      fi
      sleep 1
      ELAPSED=$((ELAPSED + 1))
    done

    if [ "${TAKEOVER_SUCCESS}" != true ]; then
      echo "::error::Failover takeover timed out after ${MAX_WAIT} seconds!" >&2
      exit 1
    fi
  fi
}

# 5. Post-Failover Verification & Fingerprint Comparison
post_failover_verify() {
  echo "[5/5] Post-Failover Verification..."
  if [ "$DRY_RUN" = true ]; then
    echo "  [DRY-RUN] Verifying 2/2 Replicas Ready, Lease acquired, and Target Fingerprints unchanged."
    echo "Dry-run failover procedure validation completed successfully."
  else
    echo "  - Verifying 2/2 Replicas Ready..."
    kubectl rollout status deployment/platform-ownership-guard-controller-manager \
      -n "${GUARD_NAMESPACE}" --timeout=120s
    READY_REPLICAS=$(kubectl get deployment -n "${GUARD_NAMESPACE}" platform-ownership-guard-controller-manager -o jsonpath='{.status.readyReplicas}')
    if [[ "${READY_REPLICAS}" != "2" ]]; then
      echo "::error::Post-failover ready replicas is ${READY_REPLICAS}/2 (expected 2)!" >&2
      exit 1
    fi

    echo "  - Verifying Workload Fingerprint after failover..."
    FINGERPRINT_AFTER=$(compute_workload_fingerprint)
    echo "  - Workload Spec Fingerprint (After):  ${FINGERPRINT_AFTER}"

    if [[ "${FINGERPRINT_BEFORE}" != "${FINGERPRINT_AFTER}" ]]; then
      echo "::error::Workload fingerprint changed! Zero Target Mutation Contract VIOLATED!" >&2
      exit 1
    fi
    echo "  - Zero Target Mutation verified: Fingerprints match 100%."
    echo "Live failover rehearsal completed successfully."
  fi
}

preflight_capacity_check
record_lease_baseline
fingerprint_workloads_before
execute_failover
post_failover_verify
