#!/usr/bin/env bash

set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_directory=$(mktemp -d)
cleanup() {
  find "$test_directory" -type f -delete
  rmdir "$test_directory/bin"
  rmdir "$test_directory"
}
trap cleanup EXIT
mkdir "$test_directory/bin"
cat >"$test_directory/bin/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
test "${npm_config_fetch_timeout:-}" = 60000
attempt=0
if [[ -f "$NPM_AUDIT_TEST_STATE" ]]; then
  attempt=$(<"$NPM_AUDIT_TEST_STATE")
fi
attempt=$((attempt + 1))
printf '%s\n' "$attempt" >"$NPM_AUDIT_TEST_STATE"
case "$NPM_AUDIT_TEST_MODE" in
  vulnerable)
    printf '# npm audit report\n1 high severity vulnerability\n' >&2
    exit 1
    ;;
  transient|unavailable)
    printf 'npm error audit endpoint returned an error\n' >&2
    exit 1
    ;;
esac
EOF
cat >"$test_directory/bin/npx" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
test "${npm_config_fetch_timeout:-}" = 120000
test "$*" = '--yes npm@11.19.1 audit --omit=dev --audit-level=high'
printf '1\n' >"$NPM_AUDIT_TEST_STATE-fallback"
if [[ "$NPM_AUDIT_TEST_MODE" == unavailable ]]; then
  printf 'npm error audit endpoint returned an error\n' >&2
  exit 1
fi
printf 'found 0 vulnerabilities\n'
EOF
chmod 0755 "$test_directory/bin/npm" "$test_directory/bin/npx"
run_audit() {
  NPM_AUDIT_TEST_MODE=$1 NPM_AUDIT_TEST_STATE=$2 \
    PATH="$test_directory/bin:$PATH" \
    "$repository_root/.github/scripts/npm-audit.sh" high
}
vulnerable_state="$test_directory/vulnerable-state"
if run_audit vulnerable "$vulnerable_state"; then
  printf 'vulnerability result was accepted\n' >&2
  exit 1
fi
test "$(<"$vulnerable_state")" = 1
test ! -e "$vulnerable_state-fallback"
transient_state="$test_directory/transient-state"
run_audit transient "$transient_state"
test "$(<"$transient_state")" = 1
test "$(<"$transient_state-fallback")" = 1
unavailable_state="$test_directory/unavailable-state"
if run_audit unavailable "$unavailable_state"; then
  printf 'persistent registry failure was accepted\n' >&2
  exit 1
fi
test "$(<"$unavailable_state")" = 1
test "$(<"$unavailable_state-fallback")" = 1
printf 'PASS npm audit retry policy\n'
