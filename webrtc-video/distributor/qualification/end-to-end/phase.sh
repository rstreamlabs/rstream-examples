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
