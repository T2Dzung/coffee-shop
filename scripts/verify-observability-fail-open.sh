#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/coffeeshop-dev.yaml}"

NAMESPACE="${NAMESPACE:-coffeeshop}"
FIXTURE_NAME="${FIXTURE_NAME:-proxy-otel-fail-open}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-180s}"
EVIDENCE_DIR="${EVIDENCE_DIR:-${HOME}/coffeeshop-evidence/observability-fail-open/$(date -u +%Y%m%dT%H%M%SZ)}"
PORT_FORWARD_PID=""

mkdir -p "${EVIDENCE_DIR}"
cd "${PROJECT_ROOT}"

cleanup() {
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" 2>/dev/null || true
  fi
  kubectl delete service,deployment -n "${NAMESPACE}" "${FIXTURE_NAME}"     --ignore-not-found --wait=true >"${EVIDENCE_DIR}/cleanup.txt" 2>&1 || true
}
trap cleanup EXIT

for command_name in kubectl curl; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "Missing required command: ${command_name}" >&2
    exit 1
  }
done

[[ -f "${KUBECONFIG}" ]] || {
  echo "KUBECONFIG does not exist: ${KUBECONFIG}" >&2
  exit 1
}

IMAGE="$(kubectl get deployment proxy -n "${NAMESPACE}"   -o jsonpath='{.spec.template.spec.containers[0].image}')"
[[ -n "${IMAGE}" ]] || {
  echo "Could not resolve the current Proxy image." >&2
  exit 1
}

cat >"${EVIDENCE_DIR}/fixture-summary.txt" <<EOF
namespace=${NAMESPACE}
fixture=${FIXTURE_NAME}
image=${IMAGE}
otel_endpoint=127.0.0.1:1
EOF

kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${FIXTURE_NAME}
  namespace: ${NAMESPACE}
  labels:
    test.coffeeshop.io/fixture: observability-fail-open
spec:
  replicas: 1
  selector:
    matchLabels:
      test.coffeeshop.io/name: ${FIXTURE_NAME}
  template:
    metadata:
      labels:
        app: proxy
        test.coffeeshop.io/name: ${FIXTURE_NAME}
        test.coffeeshop.io/fixture: observability-fail-open
    spec:
      automountServiceAccountToken: false
      containers:
        - name: proxy
          image: ${IMAGE}
          imagePullPolicy: IfNotPresent
          env:
            - name: APP_NAME
              value: proxy-fail-open-fixture
            - name: GRPC_PRODUCT_HOST
              valueFrom:
                configMapKeyRef:
                  name: coffeeshop-config
                  key: GRPC_PRODUCT_HOST
            - name: GRPC_PRODUCT_PORT
              valueFrom:
                configMapKeyRef:
                  name: coffeeshop-config
                  key: GRPC_PRODUCT_PORT
            - name: GRPC_COUNTER_HOST
              valueFrom:
                configMapKeyRef:
                  name: coffeeshop-config
                  key: GRPC_COUNTER_HOST
            - name: GRPC_COUNTER_PORT
              valueFrom:
                configMapKeyRef:
                  name: coffeeshop-config
                  key: GRPC_COUNTER_PORT
            - name: OTEL_EXPORTER_OTLP_ENDPOINT
              value: 127.0.0.1:1
            - name: OTEL_TRACES_SAMPLER
              value: parentbased_traceidratio
            - name: OTEL_TRACES_SAMPLER_ARG
              value: "1.0"
            - name: OTEL_RESOURCE_ATTRIBUTES
              value: deployment.environment=development,test.fixture=observability-fail-open
          ports:
            - name: http
              containerPort: 5000
          readinessProbe:
            tcpSocket:
              port: http
            initialDelaySeconds: 2
            periodSeconds: 2
          resources:
            requests:
              cpu: 25m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: ${FIXTURE_NAME}
  namespace: ${NAMESPACE}
spec:
  selector:
    test.coffeeshop.io/name: ${FIXTURE_NAME}
  ports:
    - name: http
      port: 5000
      targetPort: http
EOF

kubectl rollout status deployment/"${FIXTURE_NAME}" -n "${NAMESPACE}" --timeout="${WAIT_TIMEOUT}"
kubectl get deployment,pod,service -n "${NAMESPACE}"   -l test.coffeeshop.io/fixture=observability-fail-open -o wide   >"${EVIDENCE_DIR}/fixture-resources.txt"

kubectl port-forward -n "${NAMESPACE}" service/"${FIXTURE_NAME}" 15008:5000   >"${EVIDENCE_DIR}/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!

for _ in $(seq 1 30); do
  if curl -sS -o /dev/null http://127.0.0.1:15008/ 2>/dev/null; then
    break
  fi
  sleep 1
done

HTTP_CODE="$(curl -sS -o "${EVIDENCE_DIR}/response.json" -w '%{http_code}'   http://127.0.0.1:15008/v1/api/item-types)"
[[ "${HTTP_CODE}" == "200" ]] || {
  echo "Fail-open fixture returned HTTP ${HTTP_CODE}, want 200." >&2
  exit 1
}

POD_NAME="$(kubectl get pod -n "${NAMESPACE}"   -l test.coffeeshop.io/name="${FIXTURE_NAME}" -o jsonpath='{.items[0].metadata.name}')"
kubectl logs -n "${NAMESPACE}" "${POD_NAME}" >"${EVIDENCE_DIR}/proxy.log"
RESTARTS="$(kubectl get pod -n "${NAMESPACE}" "${POD_NAME}"   -o jsonpath='{.status.containerStatuses[0].restartCount}')"
[[ "${RESTARTS}" == "0" ]] || {
  echo "Fail-open fixture restarted ${RESTARTS} times." >&2
  exit 1
}

echo "Fail-open fixture passed: HTTP 200, same Proxy image, unreachable OTLP endpoint, zero restarts."
echo "Evidence saved in ${EVIDENCE_DIR}"
