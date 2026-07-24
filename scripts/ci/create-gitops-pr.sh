#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <base-branch> <update-branch> <title> <body>" >&2
}

if [[ $# -ne 4 ]]; then
  usage
  exit 2
fi

BASE_BRANCH="$1"
UPDATE_BRANCH="$2"
TITLE="$3"
BODY="$4"

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

GITHUB_API_URL="${GITHUB_API_URL:-https://api.github.com}"

if [[ ! "${BASE_BRANCH}" =~ ^[A-Za-z0-9._/-]+$ ]] ||
  [[ ! "${UPDATE_BRANCH}" =~ ^[A-Za-z0-9._/-]+$ ]]; then
  echo "Error: invalid Git branch name." >&2
  exit 2
fi

git push origin "HEAD:refs/heads/${UPDATE_BRANCH}"

PAYLOAD="$(
  jq -n \
    --arg title "${TITLE}" \
    --arg head "${UPDATE_BRANCH}" \
    --arg base "${BASE_BRANCH}" \
    --arg body "${BODY}" \
    '{title: $title, head: $head, base: $base, body: $body}'
)"

RESPONSE_FILE="$(mktemp)"
trap 'rm -f "${RESPONSE_FILE}"' EXIT

HTTP_STATUS="$(
  curl --silent --show-error \
    --output "${RESPONSE_FILE}" \
    --write-out '%{http_code}' \
    --request POST \
    --header "Accept: application/vnd.github+json" \
    --header "Authorization: Bearer ${GITHUB_TOKEN}" \
    --header "X-GitHub-Api-Version: 2022-11-28" \
    "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/pulls" \
    --data "${PAYLOAD}"
)"

if [[ "${HTTP_STATUS}" != "201" ]]; then
  echo "Error: GitHub pull request API returned HTTP ${HTTP_STATUS}." >&2
  jq -r '.message // .' "${RESPONSE_FILE}" >&2
  exit 1
fi

PR_URL="$(jq -r '.html_url' "${RESPONSE_FILE}")"
echo "Created GitOps pull request: ${PR_URL}"
echo "gitops_pr_url=${PR_URL}" >>"${GITHUB_OUTPUT:-/dev/null}"
