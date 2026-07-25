#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPECTED_ACCOUNT_ID="${PROD_AWS_ACCOUNT_ID:-694637278772}"
EXPECTED_REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-ap-southeast-1}}"
KUBECONFIG_PATH="${PROD_KUBECONFIG:-${HOME}/.kube/coffeeshop-prod.yaml}"
NAMESPACE="coffeeshop"
WAIT_ATTEMPTS="${PROD_RESILIENCE_WAIT_ATTEMPTS:-60}"
WAIT_SECONDS="${PROD_RESILIENCE_WAIT_SECONDS:-5}"
FIXTURE_ALARM="coffeeshop-prod-resilience-fixture"
FIXTURE_METRIC_NAMESPACE="CoffeeShop/Resilience"
FIXTURE_METRIC_NAME="FailureGate"

export AWS_REGION="${EXPECTED_REGION}"
export AWS_DEFAULT_REGION="${EXPECTED_REGION}"
export KUBECONFIG="${KUBECONFIG_PATH}"

fail() {
  echo "Error: $*" >&2
  exit 1
}

wait_for_jsonpath() {
  local description="$1"
  local expected="$2"
  shift 2
  local attempt actual
  for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
    actual="$("$@" 2>/dev/null || true)"
    if [[ "${actual}" == "${expected}" ]]; then
      echo "${description}: ${expected}"
      return
    fi
    sleep "${WAIT_SECONDS}"
  done
  fail "${description} did not become ${expected}"
}

wait_for_alarm_state() {
  local expected_state="$1"
  local attempt actual_state
  for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
    actual_state="$(aws cloudwatch describe-alarms \
      --alarm-names "${FIXTURE_ALARM}" \
      --query 'MetricAlarms[0].StateValue' \
      --output text 2>/dev/null || true)"
    if [[ "${actual_state}" == "${expected_state}" ]]; then
      echo "CloudWatch fixture alarm reached ${expected_state}."
      return
    fi
    sleep "${WAIT_SECONDS}"
  done
  fail "CloudWatch fixture alarm did not reach ${expected_state}"
}

wait_for_job_failed_closed() {
  local job_name="$1"
  local attempt job_status
  for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
    job_status="$(kubectl get job "${job_name}" -n "${NAMESPACE}" -o json 2>/dev/null || true)"
    if jq -e 'any(.status.conditions[]?; .type == "Complete" and .status == "True")' \
      >/dev/null 2>&1 <<<"${job_status}"; then
      fail "${job_name} unexpectedly completed"
    fi
    if jq -e 'any(.status.conditions[]?; .type == "Failed" and .status == "True")' \
      >/dev/null 2>&1 <<<"${job_status}"; then
      echo "${job_name} reached Failed as expected."
      return
    fi
    sleep "${WAIT_SECONDS}"
  done
  fail "${job_name} did not reach Failed"
}

wait_for_policy_endpoint() {
  local policy_name="$1"
  local expected_pod="$2"
  local attempt endpoints
  for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
    endpoints="$(kubectl get policyendpoints.networking.k8s.aws \
      -n "${NAMESPACE}" -o json 2>/dev/null || true)"
    if jq -e --arg policy "${policy_name}" --arg pod "${expected_pod}" '
      any(.items[]?;
        .spec.policyRef.name == $policy
        and (.spec.podIsolation | index("Egress")) != null
        and any(.spec.podSelectorEndpoints[]?; .name == $pod)
      )
    ' >/dev/null 2>&1 <<<"${endpoints}"; then
      echo "VPC CNI PolicyEndpoint programmed for ${expected_pod}."
      return
    fi
    sleep "${WAIT_SECONDS}"
  done
  fail "VPC CNI did not program ${policy_name} for ${expected_pod}"
}

cleanup() {
  local exit_code=$?
  set +e
  kubectl delete externalsecret resilience-denied-secret \
    -n "${NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1
  kubectl delete secret resilience-denied-secret \
    -n "${NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1
  kubectl delete job resilience-invalid-migration resilience-db-network-denied \
    -n "${NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1
  kubectl delete networkpolicy resilience-db-network-denied \
    -n "${NAMESPACE}" --ignore-not-found --wait=false >/dev/null 2>&1
  aws cloudwatch delete-alarms --alarm-names "${FIXTURE_ALARM}" >/dev/null 2>&1
  if [[ "${exit_code}" -eq 0 ]]; then
    echo "Resilience fixtures cleaned up."
  else
    echo "Resilience fixture cleanup attempted after failure." >&2
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

caller_account="$(aws sts get-caller-identity --query Account --output text)"
[[ "${caller_account}" == "${EXPECTED_ACCOUNT_ID}" ]] || \
  fail "caller account ${caller_account} does not match PROD account ${EXPECTED_ACCOUNT_ID}"
[[ "${PROD_CONFIRM_RESILIENCE:-}" == "${EXPECTED_ACCOUNT_ID}" ]] || \
  fail "set PROD_CONFIRM_RESILIENCE=${EXPECTED_ACCOUNT_ID} to run controlled failure fixtures"
[[ -f "${KUBECONFIG_PATH}" ]] || fail "PROD kubeconfig not found: ${KUBECONFIG_PATH}"

for application in coffeeshop-prod-platform coffeeshop-prod; do
  [[ "$(kubectl get application "${application}" -n argocd \
    -o jsonpath='{.status.sync.status}')" == "Synced" ]] || \
    fail "Argo Application ${application} is not Synced before resilience testing"
  [[ "$(kubectl get application "${application}" -n argocd \
    -o jsonpath='{.status.health.status}')" == "Healthy" ]] || \
    fail "Argo Application ${application} is not Healthy before resilience testing"
done

migration_image="$(
  awk '
    $1 == "-" && $2 == "name:" { selected = ($3 == "go-coffeeshop-migrate") }
    selected && $1 == "newName:" { repository = $2 }
    selected && $1 == "digest:" { print repository "@" $2; exit }
  ' "${PROJECT_ROOT}/infrastructure/k8s/apps/coffeeshop/overlays/prod/kustomization.yaml"
)"
[[ "${migration_image}" =~ @sha256:[0-9a-f]{64}$ ]] || \
  fail "could not resolve the immutable PROD migration image"

echo "=== F1: ESO exact-ARN denial ==="
cat <<EOF | kubectl apply -f -
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: resilience-denied-secret
  namespace: ${NAMESPACE}
spec:
  refreshInterval: 5s
  secretStoreRef:
    kind: ClusterSecretStore
    name: aws-secretsmanager
  target:
    name: resilience-denied-secret
  data:
    - secretKey: denied
      remoteRef:
        key: /coffeeshop/prod/resilience-denied-fixture
EOF

denial_observed=false
for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
  denial_status="$(kubectl get externalsecret resilience-denied-secret \
    -n "${NAMESPACE}" -o json 2>/dev/null || true)"
  denial_events="$(kubectl get events -n "${NAMESPACE}" \
    --field-selector involvedObject.name=resilience-denied-secret \
    -o json 2>/dev/null || true)"
  if jq -e '
    any(.status.conditions[]?;
      .type == "Ready"
      and .status == "False"
      and .reason == "SecretSyncedError"
    )
  ' >/dev/null 2>&1 <<<"${denial_status}" &&
    jq -e '
      any(.items[]?;
        .reason == "UpdateFailed"
        and ((.message // "") | test("AccessDeniedException|not authorized"; "i"))
      )
    ' >/dev/null 2>&1 <<<"${denial_events}"; then
    denial_observed=true
    break
  fi
  sleep "${WAIT_SECONDS}"
done
[[ "${denial_observed}" == "true" ]] || \
  fail "ESO did not report AccessDenied for the out-of-allowlist secret"
! kubectl get secret resilience-denied-secret -n "${NAMESPACE}" >/dev/null 2>&1 || \
  fail "ESO materialized a Secret for an out-of-allowlist ARN"
echo "F1 PASSED: ESO was denied and no Kubernetes Secret was created."
kubectl delete externalsecret resilience-denied-secret \
  -n "${NAMESPACE}" --wait=true

echo "=== F2: invalid migration fails closed ==="
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: resilience-invalid-migration
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: resilience-invalid-migration
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 90
  template:
    metadata:
      labels:
        app.kubernetes.io/name: resilience-invalid-migration
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ${migration_image}
          env:
            - name: MIGRATION_MODE
              value: migrate
            - name: PG_URL
              valueFrom:
                secretKeyRef:
                  name: coffeeshop-secret
                  key: PG_URL
            - name: MIGRATION_PATH
              value: /app/db/does-not-exist
EOF
wait_for_job_failed_closed resilience-invalid-migration
echo "F2 PASSED: invalid migration returned nonzero without changing the release."
kubectl delete job resilience-invalid-migration -n "${NAMESPACE}" --wait=true

echo "=== F3: declared database network denial ==="
counter_pod="$(kubectl get pods -n "${NAMESPACE}" -l app=counter \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "${counter_pod}" ]] || fail "counter Pod is missing before network failure fixture"
alb_hostname="$(kubectl get ingress coffeeshop-prod-alb-ingress -n "${NAMESPACE}" \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
[[ -n "${alb_hostname}" ]] || fail "ALB hostname is missing before network failure fixture"
rds_security_group="$(aws rds describe-db-instances \
  --db-instance-identifier coffeeshop-prod-db \
  --query 'DBInstances[0].VpcSecurityGroups[0].VpcSecurityGroupId' \
  --output text)"
rds_ip="$(aws ec2 describe-network-interfaces \
  --filters \
    "Name=group-id,Values=${rds_security_group}" \
    "Name=status,Values=in-use" \
  --query 'NetworkInterfaces[0].PrivateIpAddress' \
  --output text)"
[[ "${rds_ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || \
  fail "could not discover the private RDS IPv4 address from its security group"
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: resilience-db-network-denied
  namespace: ${NAMESPACE}
spec:
  podSelector:
    matchLabels:
      app: counter
  policyTypes:
    - Egress
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - ${rds_ip}/32
EOF
wait_for_policy_endpoint resilience-db-network-denied "${counter_pod}"

db_denial_observed=false
for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
  order_status="$(curl --silent --show-error --max-time 10 \
    -o /dev/null \
    -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    -d '{"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef","timestamp":"2026-07-25T00:00:00Z","baristaItems":[{"itemType":0}]}' \
    "http://${alb_hostname}/api/v1/api/orders" 2>/dev/null || true)"
  if [[ ! "${order_status}" =~ ^2[0-9][0-9]$ ]]; then
    db_denial_observed=true
    break
  fi
  sleep "${WAIT_SECONDS}"
done
[[ "${db_denial_observed}" == "true" ]] || \
  fail "counter still completed DB-backed orders after the RDS-deny PolicyEndpoint was programmed"
kubectl delete networkpolicy resilience-db-network-denied -n "${NAMESPACE}" --wait=true

db_recovery_observed=false
for ((attempt = 1; attempt <= WAIT_ATTEMPTS; attempt++)); do
  if curl --fail --silent --show-error --max-time 15 \
    -H 'Content-Type: application/json' \
    -d '{"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef","timestamp":"2026-07-25T00:01:00Z","baristaItems":[{"itemType":0}]}' \
    "http://${alb_hostname}/api/v1/api/orders" |
    jq -e 'type == "object"' >/dev/null 2>&1; then
    db_recovery_observed=true
    break
  fi
  sleep "${WAIT_SECONDS}"
done
[[ "${db_recovery_observed}" == "true" ]] || \
  fail "counter order path did not recover after deleting the RDS network failure fixture"
echo "F3 PASSED: counter lost only RDS egress while the declared policy was active, then recovered."

echo "=== F4: RabbitMQ single-Pod loss and quorum recovery ==="
rabbit_pod="coffeeshop-rabbitmq-server-0"
kubectl delete pod "${rabbit_pod}" -n "${NAMESPACE}" --wait=false
remaining_ready="$(kubectl get pods -n "${NAMESPACE}" \
  -l app.kubernetes.io/name=coffeeshop-rabbitmq \
  -o json | jq '
    [.items[]
      | select(.metadata.name != "coffeeshop-rabbitmq-server-0")
      | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))]
    | length
  ')"
[[ "${remaining_ready}" -ge 2 ]] || \
  fail "RabbitMQ lost quorum readiness after one Pod deletion"
kubectl wait --for=condition=Ready "pod/${rabbit_pod}" \
  -n "${NAMESPACE}" --timeout=300s
wait_for_jsonpath \
  "RabbitMQ ready replicas" "3" \
  kubectl get statefulset coffeeshop-rabbitmq-server -n "${NAMESPACE}" \
  -o jsonpath='{.status.readyReplicas}'
kubectl exec "${rabbit_pod}" -n "${NAMESPACE}" -- \
  rabbitmq-diagnostics -q check_running
echo "F4 PASSED: two peers remained Ready and the deleted broker recovered."

echo "=== F5: CloudWatch evidence and alarm transition ==="
log_streams_json="$(aws logs describe-log-streams \
  --log-group-name "/aws/containerinsights/coffeeshop-prod-eks/application" \
  --order-by LastEventTime \
  --descending \
  --limit 5 \
  --output json)"
log_stream_count="$(jq '.logStreams | length' <<<"${log_streams_json}")"
[[ "${log_stream_count}" -ge 1 ]] || \
  fail "Container Insights application log group has no streams"
recent_logs_json="$(aws logs filter-log-events \
  --log-group-name "/aws/containerinsights/coffeeshop-prod-eks/application" \
  --limit 100 \
  --output json)"
if jq -r '.events[]?.message' <<<"${recent_logs_json}" |
  grep -Eiq 'postgres(ql)?://[^[:space:]]+:[^@[:space:]]+@'; then
  fail "sampled CloudWatch application logs contain a credential-bearing PostgreSQL URI"
fi
metrics_json="$(aws cloudwatch list-metrics \
  --namespace ContainerInsights \
  --metric-name node_cpu_utilization \
  --dimensions Name=ClusterName,Value=coffeeshop-prod-eks \
  --output json)"
metric_count="$(jq '.Metrics | length' <<<"${metrics_json}")"
[[ "${metric_count}" -ge 1 ]] || \
  fail "Container Insights node_cpu_utilization metric is unavailable"

aws cloudwatch put-metric-alarm \
  --alarm-name "${FIXTURE_ALARM}" \
  --alarm-description "Temporary PROD-3 resilience transition fixture" \
  --namespace "${FIXTURE_METRIC_NAMESPACE}" \
  --metric-name "${FIXTURE_METRIC_NAME}" \
  --statistic Average \
  --period 10 \
  --evaluation-periods 1 \
  --datapoints-to-alarm 1 \
  --threshold 0.5 \
  --comparison-operator GreaterThanThreshold \
  --treat-missing-data notBreaching
aws cloudwatch put-metric-data \
  --namespace "${FIXTURE_METRIC_NAMESPACE}" \
  --metric-data "MetricName=${FIXTURE_METRIC_NAME},Value=0,Unit=Count,StorageResolution=1"
wait_for_alarm_state OK
aws cloudwatch put-metric-data \
  --namespace "${FIXTURE_METRIC_NAMESPACE}" \
  --metric-data "MetricName=${FIXTURE_METRIC_NAME},Value=1,Unit=Count,StorageResolution=1"
wait_for_alarm_state ALARM
aws cloudwatch put-metric-data \
  --namespace "${FIXTURE_METRIC_NAMESPACE}" \
  --metric-data "MetricName=${FIXTURE_METRIC_NAME},Value=0,Unit=Count,StorageResolution=1"
wait_for_alarm_state OK
aws cloudwatch delete-alarms --alarm-names "${FIXTURE_ALARM}"

for application in coffeeshop-prod-platform coffeeshop-prod; do
  [[ "$(kubectl get application "${application}" -n argocd \
    -o jsonpath='{.status.sync.status}')" == "Synced" ]] || \
    fail "Argo Application ${application} drifted during resilience testing"
  [[ "$(kubectl get application "${application}" -n argocd \
    -o jsonpath='{.status.health.status}')" == "Healthy" ]] || \
    fail "Argo Application ${application} became unhealthy during resilience testing"
done

echo "PROD-3 RESILIENCE PASSED: ESO denial, migration/network failure, RabbitMQ recovery and CloudWatch transition verified."
