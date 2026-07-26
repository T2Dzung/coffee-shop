#!/usr/bin/env bash
# Verification Script for DEV-3 Autoscaling Implementation Contracts
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DEV_OVERLAY="${REPO_ROOT}/infrastructure/k8s/apps/coffeeshop/overlays/dev"

# Runtime helpers use the stable DEV kubeconfig contract. Lifecycle credentials and
# local paths are owned by platformctl's operator config, not sourced shell state.
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"

echo "=== [DEV-3 Verification Suite] ==="

# 1. File existence checks
echo "--- 1. Manifest & Script Files Existence ---"
for f in "${DEV_OVERLAY}/hpa-proxy.yaml" "${DEV_OVERLAY}/hpa-product.yaml" "${SCRIPT_DIR}/k6_read_journey.js" "${SCRIPT_DIR}/run_loadtest.sh"; do
    if [ -f "$f" ]; then
        REL_PATH="${f#"${REPO_ROOT}/"}"
        echo "[OK] Found file: ${REL_PATH}"
    else
        REL_PATH="${f#"${REPO_ROOT}/"}"
        echo "[FAIL] Missing file: ${REL_PATH}"
        exit 1
    fi
done

# 2. Rendered Overlay Validation (kubectl kustomize)
echo "--- 2. Kustomize Rendered Output Assertions ---"
RENDERED_YAML=$(kubectl kustomize "${DEV_OVERLAY}")

# Check rendered Deployments for Single-Writer spec.replicas omission
echo "--- 2a. Single-Writer spec.replicas Omission in Rendered Overlay ---"
PROXY_DEPLOY=$(echo "${RENDERED_YAML}" | awk '/^kind: Deployment/{p=1} p{print} /^---/{p=0}' | awk '/name: proxy/{p=1} p{print} /^---/{p=0}')
PRODUCT_DEPLOY=$(echo "${RENDERED_YAML}" | awk '/^kind: Deployment/{p=1} p{print} /^---/{p=0}' | awk '/name: product/{p=1} p{print} /^---/{p=0}')

if echo "${PROXY_DEPLOY}" | grep -q "replicas:"; then
    echo "[FAIL] Rendered proxy Deployment still contains spec.replicas!"
    exit 1
else
    echo "[OK] Rendered proxy Deployment correctly omits spec.replicas"
fi

if echo "${PRODUCT_DEPLOY}" | grep -q "replicas:"; then
    echo "[FAIL] Rendered product Deployment still contains spec.replicas!"
    exit 1
else
    echo "[OK] Rendered product Deployment correctly omits spec.replicas"
fi

# 3. Assert HPAs are present in Rendered Output
echo "--- 2b. HPA Resources in Rendered Overlay ---"
if echo "${RENDERED_YAML}" | grep -q "kind: HorizontalPodAutoscaler"; then
    echo "[OK] HorizontalPodAutoscaler resources rendered in DEV overlay"
else
    echo "[FAIL] HorizontalPodAutoscaler missing in rendered DEV overlay!"
    exit 1
fi

# 4. Assert Resource Requests & Readiness Probes in Base/Rendered Deployments
echo "--- 2c. Resource Requests & Probes Assertions ---"
for svc in proxy product web; do
    if echo "${RENDERED_YAML}" | grep -A 20 "name: ${svc}" | grep -q "requests:"; then
        echo "[OK] Resource requests present for ${svc}"
    else
        echo "[FAIL] Resource requests missing for ${svc}"
        exit 1
    fi

    if echo "${RENDERED_YAML}" | grep -A 30 "name: ${svc}" | grep -q "readinessProbe:"; then
        echo "[OK] Readiness probe present for ${svc}"
    else
        echo "[FAIL] Readiness probe missing for ${svc}"
        exit 1
    fi
done

echo "=== [DEV-3 Verification Suite Passed Cleanly] ==="
