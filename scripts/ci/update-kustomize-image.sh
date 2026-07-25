#!/usr/bin/env bash
set -Eeuo pipefail

overlay_file="${1:?overlay file is required}"
service="${2:?service is required}"
image="${3:?image repository is required}"
digest="${4:?sha256 digest is required}"

[[ "${service}" =~ ^(barista|counter|kitchen|migrate|product|proxy|web)$ ]] || {
  echo "Unsupported service: ${service}" >&2
  exit 2
}
[[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo "Invalid image digest: ${digest}" >&2
  exit 2
}

temporary_file="$(mktemp)"
trap 'rm -f -- "${temporary_file}"' EXIT

awk \
  -v target="go-coffeeshop-${service}" \
  -v image="${image}" \
  -v digest="${digest}" '
  $0 ~ "^[[:space:]]*- name:[[:space:]]*" target "[[:space:]]*$" {
    in_target = 1
    found_name++
    print
    next
  }
  in_target && /^[[:space:]]*- name:/ {
    in_target = 0
  }
  in_target && /^[[:space:]]+newName:/ {
    sub(/newName:.*/, "newName: " image)
    found_image++
  }
  in_target && /^[[:space:]]+digest:/ {
    sub(/digest:.*/, "digest: " digest)
    found_digest++
  }
  { print }
  END {
    if (found_name != 1 || found_image != 1 || found_digest != 1) {
      exit 42
    }
  }
' "${overlay_file}" >"${temporary_file}" || {
  echo "Could not update exactly one image entry for ${service}." >&2
  exit 1
}

mv -- "${temporary_file}" "${overlay_file}"
trap - EXIT
