#!/usr/bin/env bash
set -Eeuo pipefail

source_commit="${1:-}"
baseline_commit="${2:-}"
default_branch_ref="${3:-}"
changed_files_path="${4:-}"
patch_path="${5:-}"

if [[ ! "${source_commit}" =~ ^[0-9a-f]{40}$ ]] ||
  [[ ! "${baseline_commit}" =~ ^[0-9a-f]{40}$ ]] ||
  [[ -z "${default_branch_ref}" ]] ||
  [[ -z "${changed_files_path}" ]] ||
  [[ -z "${patch_path}" ]]; then
  echo "Usage: validate-emergency-lineage.sh <source-commit> <baseline-commit> <default-branch-ref> <changed-files-path> <patch-path>" >&2
  exit 2
fi

git cat-file -e "${source_commit}^{commit}"
git cat-file -e "${baseline_commit}^{commit}"
git cat-file -e "${default_branch_ref}^{commit}"

if ! git merge-base --is-ancestor "${baseline_commit}" "${source_commit}"; then
  echo "Emergency baseline ${baseline_commit} is not an ancestor of source ${source_commit}." >&2
  exit 1
fi

git diff --name-only "${baseline_commit}" "${source_commit}" >"${changed_files_path}"
git diff --binary "${baseline_commit}" "${source_commit}" >"${patch_path}"
if [[ ! -s "${patch_path}" ]]; then
  echo "Emergency source ${source_commit} has no changes from baseline ${baseline_commit}." >&2
  exit 1
fi

if git merge-base --is-ancestor "${source_commit}" "${default_branch_ref}"; then
  echo "true"
  exit 0
fi

git apply --check "${patch_path}"
echo "false"
