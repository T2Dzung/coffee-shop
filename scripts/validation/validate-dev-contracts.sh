#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEV_TF_DIR="${PROJECT_ROOT}/infrastructure/terraform/envs/dev"

fail() {
  echo "DEV validation failed: $*" >&2
  exit 1
}

# This is a DEV ownership contract, not a generic AWS-account invariant.
grep -Fq '[aws_ecr_repository.platform_ownership_guard.arn]' "${DEV_TF_DIR}/iam.tf" || \
  fail "node ECR pull policy does not include PlatformOwnershipGuard"

# Do not pin a maintainer account or a temporary feature branch in validation.
# The rendered image/digest and ArgoCD health are validated by the DEV platform checks.

echo "DEV static contracts passed."
