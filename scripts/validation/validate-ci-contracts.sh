#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="${PROJECT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
CD_WORKFLOW="${PROJECT_ROOT}/.github/workflows/cd.yml"
GUARD_WORKFLOW="${PROJECT_ROOT}/.github/workflows/platform-ownership-guard.yml"
ARC_RUNNER_VALUES="${PROJECT_ROOT}/infrastructure/k8s/gitops/addons/arc/runner-values.yaml"
GUARD_RELEASE_DOCKERFILE="${PROJECT_ROOT}/platform-ownership-guard/Dockerfile.release"

fail() {
  echo "Error: $1" >&2
  exit 1
}

# ARC runner Pods share one Longhorn RWX volume. Matrix services must never
# share writable Go cache directories; this contract prevents recurrence of
# the 2026-07-18 concurrent module-lock incident.
# shellcheck disable=SC2016
if ! grep -Fq 'GOMODCACHE: /go-cache/mod/${{ matrix.service }}' "${CD_WORKFLOW}" ||
  ! grep -Fq 'GOCACHE: /go-cache/build/${{ matrix.service }}' "${CD_WORKFLOW}" ||
  ! grep -Fq 'TRIVY_CACHE_DIR: ${{ runner.temp }}/trivy-cache/${{ matrix.service }}' "${CD_WORKFLOW}" ||
  ! grep -Fq 'mountPath: /go-cache' "${ARC_RUNNER_VALUES}" ||
  grep -Fq 'GOMODCACHE: /go/pkg/mod' "${CD_WORKFLOW}"; then
  fail "ARC/CD service-partitioned cache contract is not preserved."
fi

# Application Dockerfiles package the already-built binary. Persistent
# BuildKit cache has little value here, and the scanned image must be pushed
# without invoking a second image build.
if grep -Eq 'cache-(from|to):[[:space:]]*type=gha' "${CD_WORKFLOW}"; then
  fail "Application CD must not use the legacy GitHub BuildKit cache."
fi

if [[ "$(grep -Fc 'uses: docker/build-push-action@' "${CD_WORKFLOW}")" -ne 1 ]]; then
  fail "Application CD must build the Docker image exactly once."
fi

# Match the literal shell variables embedded in the workflow run block.
# shellcheck disable=SC2016
for push_contract in \
  'docker tag "${LOCAL_IMAGE}" "${ECR_IMAGE}:${REVISION}"' \
  'docker push "${ECR_IMAGE}:${REVISION}"'; do
  if ! grep -Fq "${push_contract}" "${CD_WORKFLOW}"; then
    fail "Application CD is missing scan-once/push-same-image contract: ${push_contract}"
  fi
done

if grep -Fq "'.github/workflows/**'" "${CD_WORKFLOW}" ||
  ! grep -Fq "'.github/workflows/cd.yml'" "${CD_WORKFLOW}"; then
  fail "Application CD workflow path filter is broader than its real dependencies."
fi

# Pull-request code remains isolated on GitHub-hosted runners. Only the trusted
# push supply-chain job may enter the DEV ARC runner boundary.
GUARD_TEST_JOB=$(sed -n '/^  test:/,/^  build-and-supply-chain:/p' "${GUARD_WORKFLOW}")
GUARD_BUILD_JOB=$(sed -n '/^  build-and-supply-chain:/,$p' "${GUARD_WORKFLOW}")

if ! grep -Fq 'runs-on: ubuntu-latest' <<<"${GUARD_TEST_JOB}" ||
  ! grep -Fq 'cache: true' <<<"${GUARD_TEST_JOB}" ||
  ! grep -Fq 'cache-dependency-path: platform-ownership-guard/go.sum' <<<"${GUARD_TEST_JOB}"; then
  fail "Guard PR test must remain GitHub-hosted with its nested Go cache enabled."
fi

if ! grep -Fq 'if: github.event_name == '\''push'\''' <<<"${GUARD_BUILD_JOB}" ||
  ! grep -Fq 'runs-on: coffeeshop-runner-v3' <<<"${GUARD_BUILD_JOB}"; then
  fail "Guard trusted supply-chain job must run on the DEV ARC runner."
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
