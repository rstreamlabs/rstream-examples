#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-source-network-run.XXXXXX")"
trap 'rm -rf "${temporary_directory}"' EXIT INT TERM

qualification_directory="${temporary_directory}/qualification"
mkdir -p "${qualification_directory}/end-to-end" "${qualification_directory}/source-network"
cp "${script_directory}/run.sh" "${script_directory}/report.jq" "${qualification_directory}/source-network/"
cp "${script_directory}/fixtures/end-to-end-run.sh" "${qualification_directory}/end-to-end/run.sh"
chmod +x "${qualification_directory}/end-to-end/run.sh"

RSTREAM_CONTEXT=test \
RSTREAM_DISTRIBUTOR_SOURCE_RUNS=3 \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=4000 \
  "${qualification_directory}/source-network/run.sh" "${temporary_directory}/passed"
jq -e '
  .passed == true and
  .publishable == true and
  .runs == 3 and
  .executionFailures == 0 and
  .gates.capacityResponse == true
' "${temporary_directory}/passed/summary.json" >/dev/null

status=0
RSTREAM_CONTEXT=test \
RSTREAM_DISTRIBUTOR_SOURCE_RUNS=3 \
RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=4000 \
RSTREAM_TEST_FAIL_RUN=2 \
  "${qualification_directory}/source-network/run.sh" "${temporary_directory}/failed" || status=$?
if [[ "${status}" != 1 ]]; then
  printf 'source-network runner returned %d, want 1\n' "${status}" >&2
  exit 1
fi
jq -e '
  .passed == false and
  .executionFailures == 1 and
  .gates.allRunnersSucceeded == false and
  .gates.allResultsPassed == false and
  .gates.causalNetworkEvidence == false
' "${temporary_directory}/failed/summary.json" >/dev/null

printf 'Producer-to-adapter runner tests passed\n'
