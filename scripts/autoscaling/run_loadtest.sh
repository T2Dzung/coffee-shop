#!/usr/bin/env bash
# DEV-3 k6 Loadtest, Timeline Collector & Preflight Harness
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost/api}"
MODE="${1:-smoke}" # smoke | baseline | expected-load
TIMESTAMP="$(date -u +'%Y%m%d_%H%M%S')"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
K6_SCRIPT="${SCRIPT_DIR}/k6_read_journey.js"

# Runtime helpers use the stable DEV kubeconfig contract. Lifecycle credentials and
# local paths are owned by platformctl's operator config, not sourced shell state.
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"

# Artifact evidence output directory
EVIDENCE_DIR="${REPO_ROOT}/../CV/Platform engineering/Autoscaling/DEV-3/evidence/${TIMESTAMP}_${MODE}"
mkdir -p "${EVIDENCE_DIR}"

echo "=== [DEV-3 Preflight Harness] Timestamp: ${TIMESTAMP} ==="
echo "Target Base URL: ${BASE_URL}"
echo "Execution Mode: ${MODE}"
echo "Evidence Output Dir: ${EVIDENCE_DIR}"

# 1. Tooling availability check — BLOCKING FIX [P1]
for cmd in kubectl curl jq k6 sha256sum; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "[FATAL ERROR] Required tool '$cmd' is not installed or not in PATH." | tee "${EVIDENCE_DIR}/error.log"
        echo "Please install '$cmd' before running load tests." | tee -a "${EVIDENCE_DIR}/error.log"
        exit 1
    fi
done

# SHA256 Evidence Tracking
echo "--- Script Version Tracking ---" | tee "${EVIDENCE_DIR}/preflight.log"
k6 version | tee -a "${EVIDENCE_DIR}/preflight.log"
sha256sum "${K6_SCRIPT}" "${BASH_SOURCE[0]}" | tee -a "${EVIDENCE_DIR}/preflight.log"

# 2. Cluster Preflight Checks & Baseline Capture
echo "--- 1/4 Checking & Logging Node Capacity & Pressure ---" | tee -a "${EVIDENCE_DIR}/preflight.log"
kubectl get nodes -o wide | tee -a "${EVIDENCE_DIR}/preflight.log"
kubectl top nodes 2>/dev/null | tee -a "${EVIDENCE_DIR}/preflight.log" || echo "[WARN] kubectl top nodes unavailable"

# Node Pressure Fail-fast Check
NODE_JSON=$(kubectl get nodes -o json)
PRESSURE_CONDITIONS=$(jq -r '
    .items[] as $node
    | $node.status.conditions[]
    | select((.type == "MemoryPressure" or .type == "DiskPressure") and .status == "True")
    | "\($node.metadata.name): \(.type)=\(.status)"
' <<<"${NODE_JSON}")
if [ -n "${PRESSURE_CONDITIONS}" ]; then
    {
        echo "[FATAL ERROR] Blocking node pressure detected:"
        echo "${PRESSURE_CONDITIONS}"
    } | tee "${EVIDENCE_DIR}/error.log"
    exit 1
fi
echo "[OK] No MemoryPressure or DiskPressure detected." | tee -a "${EVIDENCE_DIR}/preflight.log"

echo "--- 2/4 Checking Metrics API Status ---" | tee -a "${EVIDENCE_DIR}/preflight.log"
if kubectl get --raw /apis/metrics.k8s.io/v1beta1 &>/dev/null; then
    echo "[OK] Metrics API (v1beta1.metrics.k8s.io) is Available." | tee -a "${EVIDENCE_DIR}/preflight.log"
else
    echo "[FATAL ERROR] Metrics API is unavailable! HPA will fail." | tee -a "${EVIDENCE_DIR}/preflight.log"
    exit 1
fi

echo "--- 3/4 Checking Target Deployments & Probes ---" | tee -a "${EVIDENCE_DIR}/preflight.log"
kubectl get deployments -n coffeeshop -o wide | tee -a "${EVIDENCE_DIR}/preflight.log"

check_argocd_health() {
    local phase="$1"
    local app_json sync_status health_status

    app_json=$(kubectl get application coffeeshop-dev -n argocd -o json)
    sync_status=$(jq -r '.status.sync.status // "Unknown"' <<<"${app_json}")
    health_status=$(jq -r '.status.health.status // "Unknown"' <<<"${app_json}")
    echo "[${phase}] ArgoCD coffeeshop-dev: sync=${sync_status}, health=${health_status}" \
        | tee -a "${EVIDENCE_DIR}/preflight.log"
    if [ "${sync_status}" != "Synced" ] || [ "${health_status}" != "Healthy" ]; then
        echo "[FATAL ERROR] ArgoCD application is not Synced/Healthy." \
            | tee "${EVIDENCE_DIR}/error.log"
        return 1
    fi
}

check_argocd_health "PRE-LOAD"

TARGET_PATH="${TARGET_PATH:-/v1/api/item-types}"
echo "--- 4/4 Testing Gateway Reachability (${BASE_URL}${TARGET_PATH}) ---" | tee -a "${EVIDENCE_DIR}/preflight.log"
SMOKE_RESP=$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w "%{http_code}" "${BASE_URL}${TARGET_PATH}" || echo "000")
if [ "$SMOKE_RESP" -ne 200 ]; then
    echo "[INFO] Target ${BASE_URL} returned status ${SMOKE_RESP}. Testing auto-discovery fallbacks..." | tee -a "${EVIDENCE_DIR}/preflight.log"
    TF_DIR="${REPO_ROOT}/infrastructure/terraform/envs/dev"
    if command -v terraform &>/dev/null && [ -d "$TF_DIR" ]; then
        TF_EIP=$(terraform -chdir="$TF_DIR" output -raw active_api_endpoint 2>/dev/null || true)
        if [ -n "$TF_EIP" ]; then
            TEST_URL="http://${TF_EIP}/api"
            TEST_RESP=$(curl --connect-timeout 3 --max-time 5 -s -o /dev/null -w "%{http_code}" "${TEST_URL}${TARGET_PATH}" || echo "000")
            if [ "$TEST_RESP" -eq 200 ]; then
                BASE_URL="$TEST_URL"
                SMOKE_RESP=200
                echo "[OK] Auto-discovered BASE_URL from Terraform EIP: ${BASE_URL}" | tee -a "${EVIDENCE_DIR}/preflight.log"
            fi
        fi
    fi
    if [ "$SMOKE_RESP" -ne 200 ]; then
        # Try local port-forward endpoint http://localhost:5000/api
        TEST_URL="http://localhost:5000/api"
        TEST_RESP=$(curl --connect-timeout 2 --max-time 4 -s -o /dev/null -w "%{http_code}" "${TEST_URL}${TARGET_PATH}" || echo "000")
        if [ "$TEST_RESP" -eq 200 ]; then
            BASE_URL="$TEST_URL"
            SMOKE_RESP=200
            echo "[OK] Auto-discovered BASE_URL from local port-forward 5000: ${BASE_URL}" | tee -a "${EVIDENCE_DIR}/preflight.log"
        fi
    fi
fi

if [ "$SMOKE_RESP" -eq 200 ]; then
    export BASE_URL
    echo "[OK] Gateway reachability check passed (HTTP 200) for ${BASE_URL}." | tee -a "${EVIDENCE_DIR}/preflight.log"
else
    echo "[FATAL ERROR] Gateway check failed with HTTP status: ${SMOKE_RESP} across all endpoints." | tee -a "${EVIDENCE_DIR}/preflight.log"
    echo "[TIP] Node IP 10.20.x.x is a private VPC IP. Please pass BASE_URL using your public EIP or run port-forward:" | tee -a "${EVIDENCE_DIR}/preflight.log"
    echo "      kubectl port-forward -n coffeeshop svc/proxy 5000:5000 &" | tee -a "${EVIDENCE_DIR}/preflight.log"
    echo "      BASE_URL=\"http://localhost:5000\" bash scripts/autoscaling/run_loadtest.sh smoke" | tee -a "${EVIDENCE_DIR}/preflight.log"
    exit 1
fi

# 3. Configure Load Parameters according to Mode
case "$MODE" in
    smoke)
        export RATE=10
        export DURATION=30s
        export PRE_ALLOCATED_VUS=10
        export MAX_VUS=30
        ;;
    baseline)
        export RATE=30
        export DURATION=2m
        export PRE_ALLOCATED_VUS=30
        export MAX_VUS=60
        ;;
    expected-load)
        export RATE=60
        export DURATION=3m
        export PRE_ALLOCATED_VUS=60
        export MAX_VUS=120
        ;;
    *)
        echo "[ERROR] Unknown mode '$MODE'. Use: smoke | baseline | expected-load"
        exit 1
        ;;
esac

# 4. Background Timeline Collector (HPA, Pods, Top Nodes/Pods during load)
echo "--- Launching Background Timeline Monitor ---"
(
    MONITOR_LOG="${EVIDENCE_DIR}/timeline_monitor.log"
    echo "Timestamp,HPA_Replicas,Pod_Statuses" > "$MONITOR_LOG"
    for (( i=0; i<180; i+=5 )); do
        NOW=$(date -u +'%H:%M:%S')
        echo "=== [Timeline $NOW] ===" >> "$MONITOR_LOG"
        kubectl get hpa -n coffeeshop >> "$MONITOR_LOG" 2>&1 || true
        kubectl get pods -n coffeeshop -o wide >> "$MONITOR_LOG" 2>&1 || true
        kubectl top pods -n coffeeshop >> "$MONITOR_LOG" 2>&1 || true
        sleep 5
    done
) &
MONITOR_PID=$!

# 5. Launch k6 Test
echo "--- Launching k6 Test (${MODE}: RATE=${RATE} req/s, DURATION=${DURATION}) ---"
set +e
k6 run "${K6_SCRIPT}" 2>&1 | tee "${EVIDENCE_DIR}/k6_output.log"
K6_EXIT_CODE="${PIPESTATUS[0]}"
set -e

# Stop background monitor
kill "$MONITOR_PID" 2>/dev/null || true

# 6. Capture Post-load Stabilization Evidence & Scale-down Polling
SCALE_DOWN_TIMEOUT_SECONDS="${SCALE_DOWN_TIMEOUT_SECONDS:-180}"
SCALE_DOWN_POLL_SECONDS="${SCALE_DOWN_POLL_SECONDS:-10}"
SCALE_DOWN_DEADLINE=$((SECONDS + SCALE_DOWN_TIMEOUT_SECONDS))
SCALE_DOWN_VERIFIED=false

echo "--- Capturing Post-load Stabilization Evidence (Polling up to ${SCALE_DOWN_TIMEOUT_SECONDS}s for Scale-Down) ---" \
    | tee -a "${EVIDENCE_DIR}/post_load.log"
while [ "${SECONDS}" -lt "${SCALE_DOWN_DEADLINE}" ]; do
    NOW=$(date -u +'%H:%M:%S')
    echo "=== [Post-Load Poll $NOW] ===" | tee -a "${EVIDENCE_DIR}/post_load.log"
    HPA_JSON=$(kubectl get hpa -n coffeeshop -o json)
    kubectl get hpa -n coffeeshop | tee -a "${EVIDENCE_DIR}/post_load.log"
    kubectl get pods -n coffeeshop -o wide | tee -a "${EVIDENCE_DIR}/post_load.log" || true

    HPA_COUNT=$(jq '.items | length' <<<"${HPA_JSON}")
    if [ "${HPA_COUNT}" -eq 0 ]; then
        echo "[INFO] No HPA resources exist; scale-down verification is not applicable." \
            | tee -a "${EVIDENCE_DIR}/post_load.log"
        SCALE_DOWN_VERIFIED=true
        break
    fi

    if jq -e '[.items[] | (.status.currentReplicas // 0) <= .spec.minReplicas] | all' \
        >/dev/null <<<"${HPA_JSON}"; then
        echo "[OK] All HPAs reached minReplicas." | tee -a "${EVIDENCE_DIR}/post_load.log"
        SCALE_DOWN_VERIFIED=true
        break
    fi
    sleep "${SCALE_DOWN_POLL_SECONDS}"
done

if [ "${SCALE_DOWN_VERIFIED}" != "true" ]; then
    echo "[FATAL ERROR] HPAs did not reach minReplicas within ${SCALE_DOWN_TIMEOUT_SECONDS}s." \
        | tee -a "${EVIDENCE_DIR}/post_load.log" "${EVIDENCE_DIR}/error.log"
    exit 1
fi

check_argocd_health "POST-LOAD"

if [ "$K6_EXIT_CODE" -eq 0 ]; then
    echo "=== [DEV-3 Load Test Completed Cleanly - Evidence Saved to ${EVIDENCE_DIR}] ==="
else
    echo "=== [DEV-3 Load Test FAILED (Exit Code ${K6_EXIT_CODE}) - Evidence Saved to ${EVIDENCE_DIR}] ==="
    exit "$K6_EXIT_CODE"
fi
