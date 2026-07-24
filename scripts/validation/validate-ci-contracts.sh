#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="${PROJECT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
CD_WORKFLOW="${PROJECT_ROOT}/.github/workflows/deploy-dev.yml"
GUARD_WORKFLOW="${PROJECT_ROOT}/.github/workflows/platform-ownership-guard.yml"
DEVSECOPS_WORKFLOW="${PROJECT_ROOT}/.github/workflows/devsecops.yml"
PROD_WORKFLOW="${PROJECT_ROOT}/.github/workflows/promote-prod.yml"
COMMISSION_WORKFLOW="${PROJECT_ROOT}/.github/workflows/commission-prod.yml"
QA_WORKFLOW="${PROJECT_ROOT}/.github/workflows/approve-release-candidate.yml"
GITOPS_PR_HELPER="${PROJECT_ROOT}/scripts/ci/create-gitops-pr.sh"
DEV_OIDC_TF="${PROJECT_ROOT}/infrastructure/terraform/envs/dev/github_oidc.tf"
DEV_ECR_TF="${PROJECT_ROOT}/infrastructure/terraform/envs/dev/ecr.tf"
GUARD_RELEASE_DOCKERFILE="${PROJECT_ROOT}/platform-ownership-guard/Dockerfile.release"

fail() {
  echo "Error: $1" >&2
  exit 1
}

# Application Dockerfiles package a binary produced by the shared build contract.
for workflow in "${PROJECT_ROOT}/.github/workflows/ci.yml" "${CD_WORKFLOW}"; do
  grep -Fq 'bash scripts/ci/build-go-service.sh' "${workflow}" || \
    fail "${workflow} bypasses the shared Go build contract."
done

# PR and merge-queue validation must not encode a repository-specific branch name.
for workflow in "${PROJECT_ROOT}/.github/workflows/ci.yml" "${DEVSECOPS_WORKFLOW}"; do
  grep -Fq 'merge_group:' "${workflow}" || \
    fail "${workflow} must validate GitHub Merge Queue candidates."
  if grep -Eq 'branches:[[:space:]]*\\[[[:space:]]*(main|feat/k3s-ha)' "${workflow}"; then
    fail "${workflow} hardcodes a protected branch name."
  fi
done
if sed -n '/^on:/,/^concurrency:/p' "${DEVSECOPS_WORKFLOW}" | grep -Fq 'push:'; then
  fail "DevSecOps must not duplicate the same validation after merge."
fi

# Release publication uses the repository default branch and GitHub-hosted isolation.
grep -Fq 'github.event.repository.default_branch' "${CD_WORKFLOW}" || \
  fail "Release workflow must discover the repository default branch dynamically."
grep -Fq 'environment: dev' "${CD_WORKFLOW}" || \
  fail "Release publication must use the DEV Environment OIDC boundary."
grep -Fq 'runs-on: ubuntu-latest' "${CD_WORKFLOW}" || \
  fail "Application release artifacts must build on GitHub-hosted runners."
grep -Fq 'Reuse existing immutable artifact' "${CD_WORKFLOW}" || \
  fail "Release retries must reuse an existing source-SHA artifact."
grep -Fq 'source_digest' "${CD_WORKFLOW}" || \
  fail "Release workflow must record immutable candidate metadata."
for service in barista counter kitchen product proxy web; do
  grep -Fq "${service}" "${CD_WORKFLOW}" || \
    fail "Release workflow is missing the ${service} service candidate."
  grep -Fq "${service}" "${QA_WORKFLOW}" || \
    fail "QA workflow cannot select the ${service} service."
  grep -Fq "${service}" "${PROD_WORKFLOW}" || \
    fail "PROD workflow cannot promote the ${service} service."
done
if grep -Eq '(:latest|ECR_IMAGE}:latest)' "${CD_WORKFLOW}"; then
  fail "Release workflow must not publish mutable latest tags."
fi

for workflow in \
  "${CD_WORKFLOW}" \
  "${GUARD_WORKFLOW}" \
  "${PROD_WORKFLOW}" \
  "${COMMISSION_WORKFLOW}" \
  "${QA_WORKFLOW}"; do
  [[ -f "${workflow}" ]] || fail "Required workflow is missing: ${workflow}"
  grep -Fq 'pull-requests: write' "${workflow}" || \
    fail "${workflow} must declare narrowly scoped pull-request permission."
  grep -Fq 'scripts/ci/create-gitops-pr.sh' "${workflow}" || \
    fail "${workflow} must submit desired-state changes through the shared PR helper."
  grep -Fq 'secrets.GITOPS_PR_TOKEN' "${workflow}" || \
    fail "${workflow} must use a bot/App token so automated PR checks are triggered."
  if grep -Eq 'git push origin "HEAD:(main|\\$\\{\\{ inputs.gitops_branch \\}\\}|\\$\\{CURRENT_BRANCH\\})"' "${workflow}"; then
    fail "${workflow} bypasses protected main with a direct desired-state push."
  fi
done

# Initial PROD commissioning must be explicit and independent from DEV runtime.
for contract in \
  'environment: prod' \
  'runs-on: ubuntu-latest' \
  'commissioning-' \
  'release_mode: "commissioning"' \
  'cache-dependency-path: source/go.sum' \
  "cache: 'true'" \
  'scripts/ci/create-gitops-pr.sh'; do
  grep -Fq "${contract}" "${COMMISSION_WORKFLOW}" || \
    fail "PROD commissioning workflow is missing contract: ${contract}"
done
if grep -Eq '(DEV_ARTIFACT_READ_ROLE_ARN|environment:[[:space:]]+dev|release-candidates/)' \
  "${COMMISSION_WORKFLOW}"; then
  fail "PROD commissioning must not depend on DEV credentials, runtime, or candidate metadata."
fi
grep -Fq "exit-code: '1'" "${COMMISSION_WORKFLOW}" || \
  fail "PROD commissioning image scan must fail closed."

[[ -f "${GITOPS_PR_HELPER}" ]] || fail "Shared GitOps PR helper is missing."
grep -Fq '/pulls' "${GITOPS_PR_HELPER}" || \
  fail "Shared GitOps helper must create a GitHub pull request."

if rg -n 'targetRevision:[[:space:]]+(feat/k3s-ha|main)' \
  "${PROJECT_ROOT}/infrastructure" >/dev/null; then
  fail "Git-backed Argo CD applications must use HEAD instead of a hardcoded branch."
fi
grep -Fq 'targetRevision: HEAD' \
  "${PROJECT_ROOT}/infrastructure/k8s/environments/dev/bootstrap/root-app.yaml" || \
  fail "DEV root Application must follow the repository default branch via HEAD."
grep -Fq 'COPY ./bin/web ./web-service' "${PROJECT_ROOT}/docker/Dockerfile-web" || \
  fail "web Dockerfile/build contract is inconsistent."

# Persistent
# BuildKit cache has little value here, and the scanned image must be pushed
# without invoking a second image build.
if grep -Eq 'cache-(from|to):[[:space:]]*type=gha' "${CD_WORKFLOW}"; then
  fail "Application CD must not use the legacy GitHub BuildKit cache."
fi

if [[ "$(grep -Fc 'uses: docker/build-push-action@' "${CD_WORKFLOW}")" -ne 1 ]]; then
  fail "Application CD must build the Docker image exactly once."
fi

# Literal workflow shell expression is the contract under inspection.
# shellcheck disable=SC2016
grep -Fq 'docker push "${REMOTE_IMAGE}"' "${CD_WORKFLOW}" || \
  fail "Release workflow must push the exact locally scanned image."

if grep -Fq "'.github/workflows/**'" "${CD_WORKFLOW}" ||
  ! grep -Fq "'.github/workflows/deploy-dev.yml'" "${CD_WORKFLOW}"; then
  fail "Application CD workflow path filter is broader than its real dependencies."
fi

# Formal QA approval is recorded for one exact candidate identity.
grep -Fq 'environment: qa' "${QA_WORKFLOW}" || \
  fail "QA decision workflow must use a protected QA Environment."
for contract in source_commit source_image source_digest qa_status evidence_url; do
  grep -Fq "${contract}" "${QA_WORKFLOW}" || \
    fail "QA workflow is missing evidence field: ${contract}"
done

# PROD promotion may evaluate and copy an artifact, but never rebuild the app.
for forbidden in \
  'bash scripts/ci/build-go-service.sh' \
  'docker build' \
  'docker/build-push-action@'; do
  if grep -Fq "${forbidden}" "${PROD_WORKFLOW}"; then
    fail "PROD promotion rebuilds the application: ${forbidden}"
  fi
done
for contract in \
  'qa_status == "approved"' \
  'DEV_ARTIFACT_READ_ROLE_ARN' \
  'crane copy' \
  'DESTINATION_DIGEST' \
  'SOURCE_DIGEST' \
  'runtime_scope' \
  'artifact-only'; do
  grep -Fq "${contract}" "${PROD_WORKFLOW}" || \
    fail "PROD zero-rebuild promotion is missing contract: ${contract}"
done

# Literal Terraform interpolation expressions are the contract under inspection.
# shellcheck disable=SC2016
grep -Fq 'repo:${var.github_repository}:environment:dev' "${DEV_OIDC_TF}" || \
  fail "DEV artifact publisher OIDC trust must bind the DEV Environment."
# shellcheck disable=SC2016
grep -Fq 'repo:${var.github_repository}:environment:prod' "${DEV_OIDC_TF}" || \
  fail "DEV candidate reader OIDC trust must bind the PROD Environment."
READER_POLICY="$(
  sed -n \
    '/resource "aws_iam_role_policy" "github_prod_artifact_reader"/,/resource "aws_iam_role_policy" "github_actions_ecr_push"/p' \
    "${DEV_OIDC_TF}"
)"
for action in ecr:BatchCheckLayerAvailability ecr:BatchGetImage ecr:GetDownloadUrlForLayer; do
  grep -Fq "${action}" <<<"${READER_POLICY}" || \
    fail "PROD candidate reader is missing ${action}."
done
if grep -Fq 'ecr:PutImage' <<<"${READER_POLICY}"; then
  fail "PROD candidate reader must not mutate DEV ECR."
fi
grep -Fq 'Resource = [for repository in aws_ecr_repository.services : repository.arn]' \
  <<<"${READER_POLICY}" || \
  fail "PROD candidate reader must be scoped to all and only DEV service repositories."
grep -Fq 'image_tag_mutability = "IMMUTABLE"' "${DEV_ECR_TF}" || \
  fail "DEV release repositories must reject source-SHA tag overwrite."

# Pull-request code remains isolated on GitHub-hosted runners. Only the trusted
# push supply-chain job may enter the DEV ARC runner boundary.
GUARD_TEST_JOB=$(sed -n '/^  test:/,/^  build-and-supply-chain:/p' "${GUARD_WORKFLOW}")
GUARD_BUILD_JOB=$(sed -n '/^  build-and-supply-chain:/,$p' "${GUARD_WORKFLOW}")

if ! grep -Fq 'runs-on: ubuntu-latest' <<<"${GUARD_TEST_JOB}" ||
  ! grep -Fq 'cache: true' <<<"${GUARD_TEST_JOB}" ||
  ! grep -Fq 'cache-dependency-path: platform-ownership-guard/go.sum' <<<"${GUARD_TEST_JOB}"; then
  fail "Guard PR test must remain GitHub-hosted with its nested Go cache enabled."
fi

if ! grep -Fq "github.event_name == 'push'" <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'github.event.repository.default_branch' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'environment: dev' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'runs-on: coffeeshop-runner-v3' <<<"${GUARD_BUILD_JOB}"; then
  fail "Guard trusted supply-chain job must use default-branch and DEV Environment boundaries."
fi

# The Guard release follows the same proven boundary as the application CD:
# compile with a branch-partitioned Longhorn Go cache, then let Docker package
# the pre-built binary without a second Go build inside ephemeral DinD.
# shellcheck disable=SC2016
if ! grep -Fq 'GOMODCACHE: /go-cache/mod/platform-ownership-guard/${{ github.ref_name }}' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'GOCACHE: /go-cache/build/platform-ownership-guard/${{ github.ref_name }}' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'cache: false' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'go build -mod=readonly -trimpath -ldflags="-s -w" -o bin/manager ./cmd/main.go' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'file: platform-ownership-guard/Dockerfile.release' <<<"${GUARD_BUILD_JOB}"; then
  fail "Guard release must compile with its partitioned local Go cache before Docker packaging."
fi

if ! grep -Fq 'COPY --chown=65532:65532 bin/manager /manager' "${GUARD_RELEASE_DOCKERFILE}" ||
  grep -Eq '^FROM[[:space:]]+golang:' "${GUARD_RELEASE_DOCKERFILE}"; then
  fail "Guard release Dockerfile must package the pre-built binary without compiling Go."
fi

for guard_contract in \
  'push: true' \
  'steps.build-image.outputs.digest' \
  'cosign sign --yes' \
  'cosign verify' \
  'kustomize edit set image'; do
  if ! grep -Fq "${guard_contract}" "${GUARD_WORKFLOW}"; then
    fail "Guard workflow is missing contract: ${guard_contract}"
  fi
done

if grep -Fq 'continue-on-error: true' "${GUARD_WORKFLOW}"; then
  fail "Guard supply-chain gates must fail closed."
fi

if grep -Eq 'cache-(from|to):[[:space:]]*type=gha' "${GUARD_WORKFLOW}"; then
  fail "Guard workflow must not introduce GitHub BuildKit cache."
fi

if grep -Eq '^[[:space:]]+uses:[[:space:]]+[^@[:space:]]+@v[0-9]' "${GUARD_WORKFLOW}"; then
  fail "Guard workflow Actions must be pinned by full commit SHA."
fi

# Validate Phase 6.8 Rehearsal Scripts Contract
REHEARSAL_DIR="${PROJECT_ROOT}/scripts/rehearsal"
for script in rehearse-ha-failover.sh rehearse-upgrade-rollback.sh rehearse-removal.sh; do
  SCRIPT_PATH="${REHEARSAL_DIR}/${script}"
  if [[ ! -f "${SCRIPT_PATH}" ]]; then
    fail "Rehearsal script ${script} is missing."
  fi
  if ! grep -Fq 'set -euo pipefail' "${SCRIPT_PATH}" ||
     ! grep -Fq -- '--dry-run' "${SCRIPT_PATH}"; then
    fail "Rehearsal script ${script} must enforce strict mode and dry-run safety flags."
  fi
done

if ! grep -Fq -- '--confirm-destructive' "${REHEARSAL_DIR}/rehearse-ha-failover.sh" ||
  ! grep -Fq 'MAX_WAIT=45' "${REHEARSAL_DIR}/rehearse-ha-failover.sh" ||
  ! grep -Fq 'compute_workload_fingerprint' "${REHEARSAL_DIR}/rehearse-ha-failover.sh"; then
  fail "HA rehearsal must retain confirmation, bounded polling and target fingerprint checks."
fi

for checkpoint_script in rehearse-upgrade-rollback.sh rehearse-removal.sh; do
  if ! grep -Fq -- '--checkpoint' "${REHEARSAL_DIR}/${checkpoint_script}" ||
    ! grep -Fq -- '--evidence-dir' "${REHEARSAL_DIR}/${checkpoint_script}" ||
    grep -Eq 'kubectl[[:space:]]+(apply|delete|patch|replace|scale|set)' "${REHEARSAL_DIR}/${checkpoint_script}"; then
    fail "${checkpoint_script} must be a read-only, checkpoint-based evidence helper."
  fi
done

echo "CI/CD architecture contract validation completed successfully."
