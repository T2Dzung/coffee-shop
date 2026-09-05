#!/bin/sh
# Deterministic native HTTP leaf. Credentials remain inside this lab-only Pod.
set -eu
exec curl --fail --silent --show-error --max-time 20 \
  --user "${RABBIT_USER:?}:${RABBIT_PASS:?}" "$@"
