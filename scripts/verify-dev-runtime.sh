#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
VENV_DIR="${CONTROL_VENV_DIR:-${HOME}/.venvs/go-coffeeshop-platform}"
export PATH="${HOME}/.local/bin:${VENV_DIR}/bin:${PATH}"
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"
: "${TF_DATA_DIR:=${HOME}/.cache/go-coffeeshop/terraform/dev}"
export TF_DATA_DIR

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
require_command jq

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

for namespace in kube-system argocd coffeeshop cnpg-system longhorn-system monitoring actions-runner-system platform-ownership-guard-system; do
  check_namespace_pods "${namespace}"
done

guard_namespace="platform-ownership-guard-system"
guard_app="platform-ownership-guard"
guard_deployment="platform-ownership-guard-controller-manager"
guard_service="platform-ownership-guard-metrics-service"
guard_service_account="system:serviceaccount:${guard_namespace}:platform-ownership-guard-controller-manager"

echo "Verifying PlatformOwnershipGuard runtime contract..."

if ! namespace_exists "${guard_namespace}"; then
  record_failure "PlatformOwnershipGuard namespace is missing"
fi

if ! kubectl get applications.argoproj.io -n argocd "${guard_app}" \
    > "${EVIDENCE_DIR}/guard-argocd-app.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard ArgoCD Application is missing"
fi

if ! guard_crd_json="$(kubectl get crd ownershipaudits.guard.platform.t2dzung.github.io -o json 2>/dev/null)"; then
  record_failure "OwnershipAudit CRD is missing"
else
  printf '%s\n' "${guard_crd_json}" > "${EVIDENCE_DIR}/guard-crd.json"
  if ! jq -e '
      (.spec.versions | length == 1) and
      (.spec.versions[0].name == "v1alpha1") and
      (.spec.versions[0].served == true) and
      (.spec.versions[0].storage == true) and
      (.status.storedVersions == ["v1alpha1"])
    ' >/dev/null 2>&1 <<<"${guard_crd_json}"; then
    record_failure "OwnershipAudit CRD must have only served/storage v1alpha1 and storedVersions=[v1alpha1]"
  fi
fi

if kubectl get crd ownershipaudits.guard.platform.t2dzung.github.io >/dev/null 2>&1 &&
  ! kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership -o yaml \
    > "${EVIDENCE_DIR}/guard-ownershipaudit.yaml" 2>/dev/null; then
  record_failure "coffeeshop/coffeeshop-ownership audit is missing"
elif kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership >/dev/null 2>&1; then
  audit_ready="$(kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')"
  inventory_ready="$(kubectl get ownershipaudit -n coffeeshop coffeeshop-ownership \
    -o jsonpath='{.status.conditions[?(@.type=="InventoryReady")].status}')"
  if [[ "${audit_ready}" != "True" || "${inventory_ready}" != "True" ]]; then
    record_failure "OwnershipAudit is not Ready/InventoryReady (Ready=${audit_ready:-Missing}, InventoryReady=${inventory_ready:-Missing})"
  fi
fi

if ! kubectl get deployment -n "${guard_namespace}" "${guard_deployment}" -o yaml \
    > "${EVIDENCE_DIR}/guard-deployment.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard Deployment is missing"
else
  guard_image="$(kubectl get deployment -n "${guard_namespace}" "${guard_deployment}" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="manager")].image}')"
  guard_desired="$(kubectl get deployment -n "${guard_namespace}" "${guard_deployment}" \
    -o jsonpath='{.spec.replicas}')"
  guard_ready="$(kubectl get deployment -n "${guard_namespace}" "${guard_deployment}" \
    -o jsonpath='{.status.readyReplicas}')"
  if [[ ! "${guard_image}" =~ @sha256:[a-f0-9]{64}$ ]] ||
    [[ "${guard_image}" =~ @sha256:0{64}$ ]]; then
    record_failure "PlatformOwnershipGuard Deployment is not pinned to a non-placeholder digest"
  fi
  if [[ "${guard_desired:-0}" != "2" ]]; then
    record_failure "PlatformOwnershipGuard Deployment spec.replicas must be 2 for HA contract (got ${guard_desired:-0})"
  fi
  if [[ "${guard_ready:-0}" != "2" ]]; then
    record_failure "PlatformOwnershipGuard Deployment has ${guard_ready:-0}/2 replicas ready"
  fi
fi

# Verify HA Pod readiness and classify node placement. ScheduleAnyway permits a
# degraded same-node placement, so record it without turning topology preference
# into a hard scheduling contract.
guard_pods_json="$(kubectl get pods -n "${guard_namespace}" \
  -l control-plane=controller-manager,app.kubernetes.io/name=platform-ownership-guard \
  -o json 2>/dev/null || true)"
printf '%s\n' "${guard_pods_json}" > "${EVIDENCE_DIR}/guard-pods.json"
guard_ready_pods="$(jq -r '
  .items[]?
  | select(.status.phase == "Running")
  | select((.status.containerStatuses // []) | length > 0)
  | select(all(.status.containerStatuses[]; .ready == true))
  | [.metadata.name, .spec.nodeName] | @tsv
' <<<"${guard_pods_json}" 2>/dev/null || true)"
printf '%s\n' "${guard_ready_pods}" > "${EVIDENCE_DIR}/guard-ready-pods.tsv"
guard_ready_pod_count="$(awk 'NF { count++ } END { print count+0 }' <<<"${guard_ready_pods}")"
if [[ "${guard_ready_pod_count}" != "2" ]]; then
  record_failure "PlatformOwnershipGuard must have exactly 2 fully Ready manager Pods (got ${guard_ready_pod_count})"
fi
guard_distinct_nodes="$(awk 'NF >= 2 { nodes[$2]=1 } END { print length(nodes)+0 }' <<<"${guard_ready_pods}")"
printf 'ready_pods=%s\ndistinct_nodes=%s\n' "${guard_ready_pod_count}" "${guard_distinct_nodes}" \
  > "${EVIDENCE_DIR}/guard-placement.txt"
if [[ "${guard_ready_pod_count}" == "2" && "${guard_distinct_nodes}" -lt 2 ]]; then
  echo "WARN: PlatformOwnershipGuard HA is running in degraded same-node placement." >&2
fi

# Verify Leader Election Lease object
if ! kubectl get lease -n "${guard_namespace}" platform-ownership-guard-leader -o yaml \
    > "${EVIDENCE_DIR}/guard-lease.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard Leader Election Lease object is missing"
else
  lease_holder="$(kubectl get lease -n "${guard_namespace}" platform-ownership-guard-leader -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
  lease_renew="$(kubectl get lease -n "${guard_namespace}" platform-ownership-guard-leader -o jsonpath='{.spec.renewTime}' 2>/dev/null || true)"
  if [[ -z "${lease_holder}" || -z "${lease_renew}" ]]; then
    record_failure "PlatformOwnershipGuard Lease is missing valid holderIdentity or renewTime"
  else
    lease_holder_pod="${lease_holder%%_*}"
    if ! awk -F '\t' -v holder="${lease_holder_pod}" '$1 == holder { found=1 } END { exit !found }' <<<"${guard_ready_pods}"; then
      record_failure "PlatformOwnershipGuard Lease holder ${lease_holder} is not one of the Ready manager Pods"
    fi
    lease_renew_epoch="$(date -u -d "${lease_renew}" +%s 2>/dev/null || true)"
    now_epoch="$(date -u +%s)"
    if [[ -z "${lease_renew_epoch}" ]] ||
      ((lease_renew_epoch > now_epoch || now_epoch - lease_renew_epoch > 60)); then
      record_failure "PlatformOwnershipGuard Lease renewTime is invalid or older than 60 seconds (${lease_renew})"
    fi
  fi
fi

# Verify PodDisruptionBudget (PDB)
if ! kubectl get pdb -n "${guard_namespace}" platform-ownership-guard-controller-manager-pdb -o yaml \
    > "${EVIDENCE_DIR}/guard-pdb.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard PodDisruptionBudget is missing"
else
  pdb_max_unavail="$(kubectl get pdb -n "${guard_namespace}" platform-ownership-guard-controller-manager-pdb -o jsonpath='{.spec.maxUnavailable}' 2>/dev/null || true)"
  if [[ "${pdb_max_unavail}" != "1" ]]; then
    record_failure "PlatformOwnershipGuard PodDisruptionBudget spec.maxUnavailable must be 1 (got ${pdb_max_unavail:-missing})"
  fi
fi

endpoint_rows="$(kubectl get endpointslice -n "${guard_namespace}" \
  -l "kubernetes.io/service-name=${guard_service}" \
  -o jsonpath='{range .items[*].endpoints[*]}{.conditions.ready}{" "}{.addresses[0]}{"\n"}{end}' 2>/dev/null || true)"
printf '%s\n' "${endpoint_rows}" > "${EVIDENCE_DIR}/guard-metrics-endpoints.txt"
if ! grep -Eq '^true[[:space:]]+[^[:space:]]+' <<<"${endpoint_rows}"; then
  record_failure "PlatformOwnershipGuard metrics Service has no ready EndpointSlice endpoint"
fi

if ! kubectl get servicemonitor -n "${guard_namespace}" \
    platform-ownership-guard-metrics-monitor -o yaml \
    > "${EVIDENCE_DIR}/guard-servicemonitor.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard ServiceMonitor is missing"
fi

if ! kubectl get prometheusrule -n monitoring platform-ownership-guard-rules -o yaml \
    > "${EVIDENCE_DIR}/guard-prometheus-rule.yaml" 2>/dev/null; then
  record_failure "PlatformOwnershipGuard PrometheusRule is missing from monitoring namespace"
fi

if ! kubectl get configmap -n monitoring -l grafana_dashboard=1 -o name 2>/dev/null |
    grep -Fq 'platform-ownership-guard-dashboard'; then
  record_failure "PlatformOwnershipGuard Grafana dashboard ConfigMap is missing"
fi

prometheus_service="$(kubectl get service -n monitoring \
  -l app.kubernetes.io/instance=monitoring,app.kubernetes.io/part-of=kube-prometheus-stack \
  -o json 2>/dev/null | jq -r '
    [.items[] | select(any(.spec.ports[]?; .port == 9090)) | .metadata.name]
    | if length == 1 then .[0] else empty end
  ' || true)"
if [[ -z "${prometheus_service}" ]]; then
  record_failure "Prometheus Service could not be discovered"
else
  prometheus_targets="$(kubectl get --raw \
    "/api/v1/namespaces/monitoring/services/http:${prometheus_service}:9090/proxy/api/v1/targets" 2>/dev/null || true)"
  printf '%s\n' "${prometheus_targets}" > "${EVIDENCE_DIR}/guard-prometheus-targets.json"
  if ! jq -e '.data.activeTargets[] | select(.labels.namespace == "platform-ownership-guard-system" and .health == "up")' \
      >/dev/null 2>&1 <<<"${prometheus_targets}"; then
    record_failure "Prometheus has no healthy PlatformOwnershipGuard scrape target"
  fi
fi

for denied_check in \
  "get secrets" \
  "list secrets" \
  "create deployments.apps" \
  "patch deployments.apps" \
  "delete deployments.apps" \
  "create replicasets.apps" \
  "patch replicasets.apps" \
  "delete replicasets.apps" \
  "create applications.argoproj.io" \
  "patch applications.argoproj.io" \
  "delete applications.argoproj.io"; do
  read -r verb resource <<<"${denied_check}"
  if [[ "$(kubectl auth can-i "${verb}" "${resource}" -n coffeeshop \
      --as="${guard_service_account}" 2>/dev/null || true)" != "no" ]]; then
    record_failure "Guard ServiceAccount must not ${verb} ${resource}"
  fi
done

if kubectl get crd clusters.postgresql.cnpg.io >/dev/null 2>&1; then
  if kubectl get cluster.postgresql.cnpg.io -n coffeeshop coffeeshop-postgres >/dev/null 2>&1; then
    kubectl get cluster.postgresql.cnpg.io -n coffeeshop coffeeshop-postgres -o yaml \
      > "${EVIDENCE_DIR}/cnpg-coffeeshop-postgres.yaml"
    kubectl get pods -n coffeeshop -l cnpg.io/cluster=coffeeshop-postgres -o wide \
      > "${EVIDENCE_DIR}/cnpg-pods.txt" || true
  fi
fi

if kubectl get crd rabbitmqclusters.rabbitmq.com >/dev/null 2>&1; then
  if kubectl get rabbitmqcluster coffeeshop-rabbitmq -n coffeeshop >/dev/null 2>&1; then
    echo "Verifying RabbitMQ HA Cluster..."
    kubectl get rabbitmqcluster coffeeshop-rabbitmq -n coffeeshop -o yaml \
      > "${EVIDENCE_DIR}/rabbitmq-cluster-status.yaml"

    # 1. Check secret annotations to prevent ArgoCD pruning
    secret_annotations="$(kubectl get secret coffeeshop-rabbitmq-default-user -n coffeeshop -o jsonpath='{.metadata.annotations}' 2>/dev/null || true)"
    if [[ -z "${secret_annotations}" ]]; then
      record_failure "Secret coffeeshop-rabbitmq-default-user has no annotations (requires Sync/Compare options to prevent ArgoCD pruning)"
    else
      if ! grep -q "compare-options" <<< "${secret_annotations}" || ! grep -q "IgnoreExtraneous" <<< "${secret_annotations}"; then
        record_failure "Secret coffeeshop-rabbitmq-default-user is missing IgnoreExtraneous compare-options annotation"
      fi
      if ! grep -q "sync-options" <<< "${secret_annotations}" || ! grep -q "Prune=false" <<< "${secret_annotations}"; then
        record_failure "Secret coffeeshop-rabbitmq-default-user is missing Prune=false sync-options annotation"
      fi
    fi

    # 2. Check replicas count from the underlying StatefulSet managed by the Operator
    ready_replicas="$(kubectl get statefulset coffeeshop-rabbitmq-server -n coffeeshop -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo '0')"
    total_replicas="$(kubectl get statefulset coffeeshop-rabbitmq-server -n coffeeshop -o jsonpath='{.spec.replicas}' 2>/dev/null || echo '3')"
    [[ -z "${ready_replicas}" ]] && ready_replicas="0"
    [[ -z "${total_replicas}" ]] && total_replicas="3"
    if [[ "${ready_replicas}" != "${total_replicas}" ]]; then
      record_failure "RabbitMQ cluster has ${ready_replicas}/${total_replicas} replicas ready"
    fi

    # 3. Check operator conditions
    reconcile_status="$(kubectl get rabbitmqcluster coffeeshop-rabbitmq -n coffeeshop -o jsonpath='{.status.conditions[?(@.type=="ReconcileSuccess")].status}' 2>/dev/null || echo 'Unknown')"
    all_ready_status="$(kubectl get rabbitmqcluster coffeeshop-rabbitmq -n coffeeshop -o jsonpath='{.status.conditions[?(@.type=="AllReplicasReady")].status}' 2>/dev/null || echo 'Unknown')"
    if [[ "${reconcile_status}" != "True" ]]; then
      record_failure "RabbitMQ cluster operator reconciliation failed (ReconcileSuccess=${reconcile_status})"
    fi
    if [[ "${all_ready_status}" != "True" ]]; then
      record_failure "RabbitMQ cluster replicas not all ready (AllReplicasReady=${all_ready_status})"
    fi

    # 4. Check queue type if server pod 0 is running and tool command is available
    if kubectl exec coffeeshop-rabbitmq-server-0 -n coffeeshop -- rabbitmqctl status >/dev/null 2>&1; then
      queues_info="$(kubectl exec coffeeshop-rabbitmq-server-0 -n coffeeshop -- rabbitmqctl list_queues name type 2>/dev/null || true)"
      if [[ -n "${queues_info}" ]]; then
        printf '%s\n' "${queues_info}" > "${EVIDENCE_DIR}/rabbitmq-queues.txt"
        queue_checked=0
        for q in counter-order-queue barista-order-queue kitchen-order-queue; do
          if grep -q "${q}" <<< "${queues_info}"; then
            queue_type="$(awk -v q_name="${q}" '$1==q_name {print $2}' <<< "${queues_info}")"
            queue_checked=$((queue_checked + 1))
            if [[ "${queue_type}" != "quorum" ]]; then
              record_failure "Queue ${q} is of type '${queue_type}' but must be 'quorum'"
            fi
          fi
        done
        if ((queue_checked == 0)); then
          record_failure "None of the coffeeshop queues (counter, barista, kitchen) were found in RabbitMQ"
        fi
      fi
    else
      echo "RabbitMQ node 0 not ready for rabbitmqctl status check." >&2
    fi
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
      echo "Terraform active_api_endpoint output is empty." \
        > "${EVIDENCE_DIR}/http-healthz.txt"
      record_failure "Terraform active_api_endpoint output is empty while VERIFY_HTTP=true"
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
