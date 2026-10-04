#!/usr/bin/env bash

set -euo pipefail

audit_level=${1:?usage: npm-audit.sh AUDIT_LEVEL}
audit_output=$(mktemp)
trap 'rm -f "$audit_output"' EXIT
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
# Node 24's bundled npm 11.17 bulk-audit transport is unreliable for large
# lockfiles. Fall back once to the pinned current npm 11 client.
printf 'npm audit transport failed; retrying with npm 11.19.1\n' >&2
if npm_config_fetch_timeout=120000 npx --yes npm@11.19.1 audit --omit=dev --audit-level="$audit_level" >"$audit_output" 2>&1; then
  cat "$audit_output"
  exit 0
else
  status=$?
fi
cat "$audit_output" >&2
exit "$status"
