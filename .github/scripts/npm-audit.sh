#!/usr/bin/env bash

set -euo pipefail

audit_level=${1:?usage: npm-audit.sh AUDIT_LEVEL}
audit_output=$(mktemp)
trap 'rm -f "$audit_output"' EXIT
for attempt in 1 2 3; do
  if npm_config_fetch_timeout=60000 npm audit --omit=dev --audit-level="$audit_level" >"$audit_output" 2>&1; then
    cat "$audit_output"
    exit 0
  else
    status=$?
  fi
  cat "$audit_output" >&2
  if ! grep -Eq 'audit endpoint returned an error|EAUDITREGISTRY|ECONNRESET|ENOTFOUND|ETIMEDOUT|EAI_AGAIN|Service Unavailable' "$audit_output"; then
    exit "$status"
  fi
  if ((attempt == 3)); then
    exit "$status"
  fi
  delay=$((attempt * 5))
  printf 'npm audit registry request failed; retrying in %ss (%s/3)\n' "$delay" "$attempt" >&2
  sleep "$delay"
done
