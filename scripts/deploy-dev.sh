#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "${1:-}" in
  plan|apply)
    echo "Legacy two-command DEV plan/apply was replaced by one reviewed saved-plan boundary." >&2
    echo "Run: ${SCRIPT_DIR}/platformctl.sh dev setup" >&2
    exit 2
    ;;
  decommission-plan|decommission-apply)
    echo "Legacy two-command DEV decommission was replaced by one reviewed saved-plan boundary." >&2
    echo "Run: ${SCRIPT_DIR}/platformctl.sh dev teardown" >&2
    exit 2
    ;;
  setup|status|teardown)
    exec bash "${SCRIPT_DIR}/platformctl.sh" dev "$@"
    ;;
  *)
    echo "Usage: $0 <setup|status|teardown> [platformctl flags]" >&2
    exit 2
    ;;
esac
