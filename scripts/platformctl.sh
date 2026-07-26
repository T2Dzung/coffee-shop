#!/usr/bin/env bash
set -Eeuo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_binary="${GO_BINARY:-$(command -v go 2>/dev/null || true)}"
[[ -n "${go_binary}" ]] || go_binary="/usr/local/go/bin/go"
[[ -x "${go_binary}" ]] || {
  echo "platformctl requires Go from the version declared in go.mod." >&2
  exit 1
}
go_binary_dir="$(dirname "${go_binary}")"
export PATH="${go_binary_dir}:${PATH}"

cache_root="${PLATFORMCTL_CACHE_DIR:-${HOME}/.cache/go-coffeeshop/platformctl}"
install -d -m 0700 "${cache_root}"
fingerprint="$(
  cd "${project_root}"
  {
    sha256sum go.mod go.sum
    find cmd/platformctl internal/platformctl -type f -name '*.go' -print0 |
      sort -z |
      xargs -0 sha256sum
  } | sha256sum | awk '{print $1}'
)"
executable="${cache_root}/platformctl-${fingerprint}"
if [[ ! -x "${executable}" ]]; then
  temporary="${executable}.tmp.$$"
  (
    cd "${project_root}"
    GOCACHE="${GOCACHE:-${cache_root}/go-build}" \
      "${go_binary}" build -trimpath -o "${temporary}" ./cmd/platformctl
  )
  chmod 0700 "${temporary}"
  mv -f "${temporary}" "${executable}"
fi

cd "${project_root}"
exec "${executable}" "$@"
