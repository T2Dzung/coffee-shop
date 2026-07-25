#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="${PROJECT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
K8S_DIR="${PROJECT_ROOT}/infrastructure/k8s"

fail() {
  echo "Kubernetes layout validation failed: $*" >&2
  exit 1
}

for required_path in \
  apps/coffeeshop/base \
  apps/coffeeshop/overlays/dev \
  apps/coffeeshop/overlays/prod \
  environments/dev/bootstrap/root-app.yaml \
  environments/dev/gitops/applications \
  environments/dev/gitops/addons \
  environments/dev/network \
  environments/dev/policies \
  environments/prod/bootstrap \
  environments/prod/platform; do
  [[ -e "${K8S_DIR}/${required_path}" ]] || fail "missing ${required_path}"
done

for obsolete_tree in bootstrap gateway gitops legacy policies prod; do
  [[ ! -e "${K8S_DIR}/${obsolete_tree}" ]] || \
    fail "obsolete top-level tree remains: ${obsolete_tree}"
done

if grep -ERn '(^|[^0-9])[0-9]{12}\.dkr\.ecr\.' \
  "${K8S_DIR}/apps/coffeeshop/base"; then
  fail "shared CoffeeShop manifests contain an environment-owned ECR account ID"
fi

grep -Fq 'path: infrastructure/k8s/environments/dev/gitops/applications' \
  "${K8S_DIR}/environments/dev/bootstrap/root-app.yaml" || \
  fail "DEV root Application does not own the DEV applications directory"
grep -Fq 'path: infrastructure/k8s/apps/coffeeshop/overlays/dev' \
  "${K8S_DIR}/environments/dev/gitops/applications/coffeeshop.yaml" || \
  fail "DEV Application does not target the DEV overlay"
grep -Fq 'path: infrastructure/k8s/apps/coffeeshop/overlays/prod' \
  "${K8S_DIR}/environments/prod/bootstrap/coffeeshop-prod-app.yaml" || \
  fail "PROD Application does not target the PROD overlay"

[[ -f "${PROJECT_ROOT}/.github/workflows/deploy-dev.yml" ]] || \
  fail "explicit DEV deployment workflow is missing"
[[ ! -e "${PROJECT_ROOT}/.github/workflows/cd.yml" ]] || \
  fail "ambiguous legacy cd.yml remains"

echo "Kubernetes shared/DEV/PROD layout contracts passed."
