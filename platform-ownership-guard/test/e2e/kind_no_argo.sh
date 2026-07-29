#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
MODULE_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
readonly MODULE_ROOT
readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-ownership-guard-v011}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node@sha256:3966ac761ae0136263ffdb6cfd4db23ef8a83cba8a463690e98317add2c9ba72}"
readonly CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-platform-ownership-guard:v0.1.1-kind}"
readonly KEEP_CLUSTER="${KEEP_KIND_CLUSTER:-false}"
readonly SERVICE_ACCOUNT="system:serviceaccount:platform-ownership-guard-system:platform-ownership-guard-controller-manager"

temporary_dir=""
cluster_created="false"

cleanup() {
  if [[ "${cluster_created}" == "true" && "${KEEP_CLUSTER}" != "true" ]]; then
    kind delete cluster --name "${CLUSTER_NAME}"
  fi
  if [[ -n "${temporary_dir}" && -d "${temporary_dir}" ]]; then
    rm -rf -- "${temporary_dir}"
  fi
}
trap cleanup EXIT

for tool in docker go kind kubectl; do
  command -v "${tool}" >/dev/null || {
    echo "required tool is unavailable: ${tool}" >&2
    exit 1
  }
done

if kind get clusters | grep -Fxq "${CLUSTER_NAME}"; then
  echo "refusing to reuse existing Kind cluster: ${CLUSTER_NAME}" >&2
  exit 1
fi

temporary_dir="$(mktemp -d)"

echo "Building controller image ${CONTROLLER_IMAGE}"
mkdir -p "${temporary_dir}/image/bin"
(
  cd "${MODULE_ROOT}"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath \
    -o "${temporary_dir}/image/bin/manager" ./cmd/main.go
)
cp "${MODULE_ROOT}/Dockerfile.release" "${temporary_dir}/image/Dockerfile.release"
docker build --file "${temporary_dir}/image/Dockerfile.release" \
  --tag "${CONTROLLER_IMAGE}" "${temporary_dir}/image"

echo "Creating no-Argo Kind cluster ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${NODE_IMAGE}" \
  --config "${SCRIPT_DIR}/kind-no-argo-cluster.yaml" --wait 120s
cluster_created="true"

kind load docker-image "${CONTROLLER_IMAGE}" --name "${CLUSTER_NAME}"
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null

kubectl apply --server-side --field-manager=kind-e2e \
  -f "${SCRIPT_DIR}/fixtures/external-crds.yaml"
kubectl wait --for=condition=Established --timeout=60s \
  crd/externalsecrets.external-secrets.io \
  crd/certificates.cert-manager.io \
  crd/certificaterequests.cert-manager.io

cp -R "${MODULE_ROOT}/config" "${temporary_dir}/config"
(
  cd "${temporary_dir}/config/default"
  "${MODULE_ROOT}/bin/kustomize" edit set image "controller=${CONTROLLER_IMAGE}"
)
"${MODULE_ROOT}/bin/kustomize" build "${temporary_dir}/config/default" |
  kubectl apply --server-side --field-manager=kind-e2e -f -

kubectl -n platform-ownership-guard-system rollout status \
  deployment/platform-ownership-guard-controller-manager --timeout=120s

assert_can_i() {
  local expected="$1"
  shift
  local actual
  actual="$(kubectl auth can-i "$@" --as="${SERVICE_ACCOUNT}" || true)"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "RBAC assertion failed: expected ${expected}, got ${actual}: kubectl auth can-i $*" >&2
    exit 1
  fi
}

assert_can_i yes get externalsecrets.external-secrets.io -n rabbitmq-system
assert_can_i yes list certificaterequests.cert-manager.io -n rabbitmq-system
assert_can_i yes watch certificates.cert-manager.io -n rabbitmq-system
assert_can_i no get secrets -n rabbitmq-system
assert_can_i no update externalsecrets.external-secrets.io -n rabbitmq-system
assert_can_i no patch certificaterequests.cert-manager.io -n rabbitmq-system
assert_can_i no delete certificates.cert-manager.io -n rabbitmq-system

if kubectl api-resources --api-group=argoproj.io --no-headers 2>/dev/null | grep -q .; then
  echo "Argo CD API must not exist in the native-only Kind cluster" >&2
  exit 1
fi

kubectl apply --server-side --field-manager=kind-e2e \
  -f "${SCRIPT_DIR}/fixtures/native-only-resources.yaml"

certificate_uid="$(kubectl -n rabbitmq-system get certificate rabbitmq-server \
  -o jsonpath='{.metadata.uid}')"
[[ -n "${certificate_uid}" ]]

kubectl -n rabbitmq-system patch certificaterequest clean-request --type=merge \
  -p "{\"metadata\":{\"ownerReferences\":[{\"apiVersion\":\"cert-manager.io/v1\",\"kind\":\"Certificate\",\"name\":\"rabbitmq-server\",\"uid\":\"${certificate_uid}\"}]}}"
kubectl -n rabbitmq-system patch certificaterequest uid-mismatch-request --type=merge \
  -p '{"metadata":{"ownerReferences":[{"apiVersion":"cert-manager.io/v1","kind":"Certificate","name":"rabbitmq-server","uid":"00000000-0000-0000-0000-000000000001"}]}}'
kubectl -n rabbitmq-system patch certificaterequest missing-owner-request --type=merge \
  -p '{"metadata":{"ownerReferences":[{"apiVersion":"cert-manager.io/v1","kind":"Certificate","name":"deleted-certificate","uid":"00000000-0000-0000-0000-000000000002"}]}}'

kubectl apply --server-side --field-manager=kind-e2e \
  -f "${SCRIPT_DIR}/fixtures/native-only-audit.yaml"
kubectl -n rabbitmq-system wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  ownershipaudit/native-only-ownership --timeout=90s

assert_audit_result() {
  local total confirmed
  total="$(kubectl -n rabbitmq-system get ownershipaudit native-only-ownership \
    -o jsonpath='{.status.summary.totalFindings}')"
  confirmed="$(kubectl -n rabbitmq-system get ownershipaudit native-only-ownership \
    -o jsonpath='{.status.summary.confirmed}')"
  if [[ "${total}" != "2" || "${confirmed}" != "2" ]]; then
    kubectl -n rabbitmq-system get ownershipaudit native-only-ownership -o yaml >&2
    echo "expected two confirmed native-only findings, got total=${total} confirmed=${confirmed}" >&2
    exit 1
  fi
}

assert_audit_result
finding_ids_before="$(kubectl -n rabbitmq-system get ownershipaudit native-only-ownership \
  -o jsonpath='{range .status.findings[*]}{.id}{"\n"}{end}')"

kubectl -n platform-ownership-guard-system rollout restart \
  deployment/platform-ownership-guard-controller-manager
kubectl -n platform-ownership-guard-system rollout status \
  deployment/platform-ownership-guard-controller-manager --timeout=120s
kubectl -n rabbitmq-system annotate ownershipaudit native-only-ownership \
  guard.platform.t2dzung.github.io/kind-rescan=after-restart --overwrite
kubectl -n rabbitmq-system wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  ownershipaudit/native-only-ownership --timeout=90s

assert_audit_result
finding_ids_after="$(kubectl -n rabbitmq-system get ownershipaudit native-only-ownership \
  -o jsonpath='{range .status.findings[*]}{.id}{"\n"}{end}')"
if [[ "${finding_ids_before}" != "${finding_ids_after}" ]]; then
  echo "finding IDs changed across controller restart" >&2
  exit 1
fi

if kubectl -n platform-ownership-guard-system logs \
  deployment/platform-ownership-guard-controller-manager --all-pods=true --prefix \
  | grep -E 'panic|forbidden|cannot list|cannot get'; then
    echo "controller logs contain runtime/RBAC failures" >&2
    exit 1
fi

echo "PASS: no-Argo Kind runtime, exact RBAC, two confirmed findings, stable restart"
