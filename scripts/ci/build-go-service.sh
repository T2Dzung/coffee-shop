#!/usr/bin/env bash
set -Eeuo pipefail

service="${1:-}"
[[ "${service}" =~ ^(barista|counter|kitchen|product|proxy|web)$ ]] || {
  echo "Usage: scripts/ci/build-go-service.sh <barista|counter|kitchen|product|proxy|web>" >&2
  exit 2
}

mkdir -p "${GOMODCACHE:-${HOME}/go/pkg/mod}" "${GOCACHE:-${HOME}/.cache/go-build}" ./bin

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -v -tags migrate -trimpath -ldflags="-w -s" \
  -o "./bin/${service}" "./cmd/${service}"
