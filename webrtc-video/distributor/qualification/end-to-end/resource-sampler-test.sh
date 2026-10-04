#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-resource-sampler.XXXXXX")"
trap 'rm -rf "${fixture_directory}"' EXIT INT TERM
control_directory="${fixture_directory}"
# Used by the sampler function loaded below.
# shellcheck disable=SC2034
resource_container_names=(producer browser)
# Load only the actual sampler, keeping Docker and the PSS reader controlled.
# shellcheck disable=SC1090
source <(awk '/^sample_container_resources\(\) \{/ {inside=1} inside {print} inside && /^}$/ {exit}' "${script_directory}/run.sh")

reset_phase() {
  printf '{"name":"baseline","startedAt":"2026-10-04T00:00:00Z"}\n' >"${control_directory}/phase.json"
}
change_phase() {
  printf '{"name":"complete","startedAt":"2026-10-04T00:00:05Z"}\n' >"${control_directory}/phase.json"
}
docker() {
  [[ "$1" == stats ]] || return 1
  if [[ "${phase_switch}" == error ]]; then return 42; fi
  if [[ "${phase_switch}" == stats ]]; then change_phase; fi
  printf '%s\n' \
    '{"Name":"producer","CPUPerc":"25.00%","MemUsage":"0B / 0B","NetIO":"1kB / 2kB","PIDs":"4"}' \
    '{"Name":"browser","CPUPerc":"50.00%","MemUsage":"1MiB / 4GiB","NetIO":"1kB / 2kB","PIDs":"4"}'
}
process_resident_sample() {
  [[ "$1" == producer ]] || return 1
  if [[ "${phase_switch}" == resident ]]; then change_phase; fi
  printf '2048 process-pss\n'
}

phase_switch=none
reset_phase
sample_container_resources >"${fixture_directory}/stable.jsonl"
jq -se 'length == 2 and all(.samplePhase.name == "baseline" and (.sampleStartedAt | type) == "string" and (.sampleFinishedAt | type) == "string") and .[0].ResidentBytes == 2048 and .[1].ResidentBytesSource == "container-cgroup"' "${fixture_directory}/stable.jsonl" >/dev/null

for phase_switch in stats resident; do
  reset_phase
  sample_container_resources >"${fixture_directory}/${phase_switch}.jsonl"
  if ! jq -se 'length == 2 and all(.samplePhase == null)' "${fixture_directory}/${phase_switch}.jsonl" >/dev/null; then
    printf 'sampler assigned a stable phase across a %s observation boundary\n' "${phase_switch}" >&2
    exit 1
  fi
done
phase_switch=error
reset_phase
if sample_container_resources >"${fixture_directory}/failed.jsonl"; then
  printf 'failed Docker measurement was accepted\n' >&2
  exit 1
fi
[[ ! -s "${fixture_directory}/failed.jsonl" ]]
printf 'Resource sampler phase-boundary tests passed\n'
