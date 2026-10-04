#!/usr/bin/env bash

write_phase_file() {
  local target=$1
  local name=$2
  local directory
  local temporary
  directory="$(dirname "${target}")"
  temporary="$(mktemp "${directory}/.phase.json.tmp.XXXXXX")"
  if ! jq -cn --arg name "${name}" --arg started_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{name: $name, startedAt: $started_at}' >"${temporary}"; then
    rm -f "${temporary}"
    return 1
  fi
  if ! mv "${temporary}" "${target}"; then
    rm -f "${temporary}"
    return 1
  fi
}

# Publish within the collector's filesystem. Replacing the host-side file
# atomically does not guarantee identical visibility through a Docker bind mount.
# Stdin carries one already-complete, non-secret phase document; completion of
# docker exec acknowledges the local rename before the phase timer continues.
copy_phase_to_container() {
  local container=$1
  local source=$2
  local target=$3
  # shellcheck disable=SC2016
  docker exec -i "${container}" sh -ceu '
    target=$1
    temporary="$(mktemp "$(dirname "$target")/.phase.json.tmp.XXXXXX")"
    trap '\''rm -f "$temporary"'\'' EXIT
    trap '\''exit 130'\'' INT
    trap '\''exit 143'\'' HUP TERM
    cat > "$temporary"
    mv "$temporary" "$target"
  ' phase-writer "${target}" <"${source}"
}
