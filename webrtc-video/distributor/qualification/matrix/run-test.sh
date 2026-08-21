#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-matrix-run-test.XXXXXX")"
cleanup() {
  find "${temporary_directory}" -type f -delete
  find "${temporary_directory}" -depth -type d -exec rmdir {} +
}
trap cleanup EXIT INT TERM

qualification_directory="${temporary_directory}/qualification"
mkdir -p \
  "${qualification_directory}/comparison" \
  "${qualification_directory}/fanout" \
  "${qualification_directory}/matrix"
cp "${script_directory}/run.sh" "${script_directory}/report.jq" "${qualification_directory}/matrix/"
cp "${script_directory}/fixtures/comparison-run.sh" "${qualification_directory}/comparison/run.sh"
cp "${script_directory}/fixtures/fanout-run.sh" "${qualification_directory}/fanout/run.sh"
chmod +x "${qualification_directory}/comparison/run.sh" "${qualification_directory}/fanout/run.sh"
status=0
RSTREAM_CONTEXT=test \
RSTREAM_DISTRIBUTOR_MATRIX_RUNS=3 \
  "${qualification_directory}/matrix/run.sh" "${temporary_directory}/output" || status=$?
if [[ "${status}" != 1 ]]; then
  printf 'matrix runner returned %d, want 1\n' "${status}" >&2
  exit 1
fi
jq -e '
  .passed == false and
  .runnerStatus == {capacity: 0, impairment: 1, fanout: 0} and
  .gates.runnerStatusMatchesReports == true and
  .gates.edgeAuthenticationEnabled == true and
  .gates.directImpairmentQualified == false and
  .productVerdict.direct == "no-go"
' "${temporary_directory}/output/summary.json" >/dev/null

printf 'Distribution matrix runner tests passed\n'
