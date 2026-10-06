#!/usr/bin/env bash
set -Eeuo pipefail
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
cat >"${fixture}/runner" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
[[ -z "$RSTREAM_CONTEXT" && "$RSTREAM_DISTRIBUTOR_CONTROL_PATH" == local && "$RSTREAM_DISTRIBUTOR_EDGE_AUTH" == false ]]
[[ "$RSTREAM_DISTRIBUTOR_MODE" == "${EXPECTED_MODE:-direct}" && "$RSTREAM_DISTRIBUTOR_EXPECT_FORMAT" == small ]]
if [[ "$RSTREAM_DISTRIBUTOR_MODE" == direct ]]; then
  [[ "$RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS" == 1500 && "$RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS" == 0 ]]
else
  [[ "$RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS" == 0 && "$RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS" == 1500 ]]
fi
hold="$EXPECTED_HOLD"
if [[ "$hold" == paired ]]; then
  case "$1" in
    *run-1|*run-4|*run-5) hold=1s ;;
    *run-2|*run-3|*run-6) hold=3s ;;
    *) exit 3 ;;
  esac
fi
grep -Fq "downHold: ${hold}" "$RSTREAM_DISTRIBUTOR_PRODUCER_CONFIG"
[[ "$CASE" != incomplete ]] || exit 2
mkdir -p "$1"
passed=true
status=0
if [[ "$CASE" == fail && "$1" == *run-2 ]]; then passed=false; status=1; fi
image=producer-image
distributor=distributor-image
if [[ "$CASE" == image-change && "$1" == *run-2 ]]; then image=changed-image; fi
if [[ "$CASE" == distributor-change && "$1" == *run-2 ]]; then distributor=changed-image; fi
mode="$RSTREAM_DISTRIBUTOR_MODE"
if [[ "$CASE" == wrong-mode && "$1" == *run-2 ]]; then mode=direct; fi
jq -n --argjson passed "$passed" --arg image "$image" --arg distributor "$distributor" --arg mode "$mode" '{passed: $passed, mode: $mode,
  profile: {controlPath: "local", edgeAuthentication: false},
  images: {producer: $image, browser: "browser-image", distributor: $distributor}}' >"$1/result.json"
exit "$status"
SCRIPT
chmod 0700 "${fixture}/runner"
for scenario in pass fail incomplete; do
  status=0
  CASE="${scenario}" EXPECTED_HOLD=1s RSTREAM_FORMAT_QUALIFICATION_RUNNER="${fixture}/runner" \
    "${script_directory}/compare-delivery.sh" 1s "${fixture}/${scenario}" || status=$?
  if [[ "${scenario}" == pass ]]; then
    [[ "${status}" == 0 ]]
    jq -e '.passed and (.runs | length == 3)' "${fixture}/${scenario}/summary.json" >/dev/null
  else
    [[ "${status}" != 0 ]]
    count=3
    [[ "${scenario}" != incomplete ]] || count=1
    jq -e --argjson count "${count}" '.passed == false and (.runs | length == $count)' "${fixture}/${scenario}/summary.json" >/dev/null
  fi
done
CASE=pass EXPECTED_HOLD=3s RSTREAM_FORMAT_QUALIFICATION_RUNNER="${fixture}/runner" \
  "${script_directory}/compare-delivery.sh" 3s "${fixture}/three-seconds"
for scenario in pass fail incomplete image-change; do
  status=0
  CASE="${scenario}" EXPECTED_HOLD=paired RSTREAM_FORMAT_QUALIFICATION_RUNNER="${fixture}/runner" \
    "${script_directory}/compare-delivery.sh" paired "${fixture}/paired-${scenario}" || status=$?
  summary="${fixture}/paired-${scenario}/summary.json"
  if [[ "${scenario}" == pass ]]; then
    [[ "${status}" == 0 ]]
    jq -e '.passed and .sameImages and .requestedRuns == 6 and
      ([.runs[].downHold] == ["1s", "3s", "3s", "1s", "1s", "3s"])' "$summary" >/dev/null
  else
    [[ "${status}" != 0 ]]
    count=6
    [[ "${scenario}" != incomplete ]] || count=1
    jq -e --argjson count "${count}" '.passed == false and (.runs | length == $count)' "$summary" >/dev/null
    if [[ "${scenario}" == image-change ]]; then jq -e '.sameImages == false' "$summary" >/dev/null; fi
  fi
done
if "${script_directory}/compare-delivery.sh" invalid "${fixture}/invalid" >/dev/null 2>&1; then exit 1; fi
[[ ! -e "${fixture}/invalid" ]]
for scenario in pass fail incomplete distributor-change wrong-mode; do
  status=0
  CASE="${scenario}" EXPECTED_HOLD=3s EXPECTED_MODE=mediamtx RSTREAM_FORMAT_QUALIFICATION_RUNNER="${fixture}/runner" \
    "${script_directory}/compare-delivery.sh" 3s "${fixture}/mediamtx-${scenario}" mediamtx || status=$?
  summary="${fixture}/mediamtx-${scenario}/summary.json"
  if [[ "${scenario}" == pass ]]; then
    [[ "${status}" == 0 ]]
    jq -e '.passed and .sameImages and .deliveryMode == "mediamtx" and .requestedRuns == 3' "$summary" >/dev/null
  else
    [[ "${status}" != 0 ]]
    jq -e '.passed == false' "$summary" >/dev/null
    if [[ "${scenario}" == distributor-change ]]; then jq -e '.sameImages == false' "$summary" >/dev/null; fi
  fi
done
if "${script_directory}/compare-delivery.sh" 3s "${fixture}/invalid-mode" unknown >/dev/null 2>&1; then exit 1; fi
[[ ! -e "${fixture}/invalid-mode" ]]
printf 'Delivery format comparison fixtures passed\n'
