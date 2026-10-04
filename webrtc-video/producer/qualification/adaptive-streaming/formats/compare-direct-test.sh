#!/usr/bin/env bash
set -Eeuo pipefail
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
cat >"${fixture}/runner" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
[[ -z "$RSTREAM_CONTEXT" && "$RSTREAM_DISTRIBUTOR_CONTROL_PATH" == local && "$RSTREAM_DISTRIBUTOR_EDGE_AUTH" == false ]]
[[ "$RSTREAM_DISTRIBUTOR_MODE" == direct && "$RSTREAM_DISTRIBUTOR_EXPECT_FORMAT" == small ]]
[[ "$RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS" == 1500 ]]
grep -Fq "downHold: ${EXPECTED_HOLD}" "$RSTREAM_DISTRIBUTOR_PRODUCER_CONFIG"
[[ "$CASE" != incomplete ]] || exit 2
mkdir -p "$1"
passed=true
status=0
if [[ "$CASE" == fail && "$1" == *run-2 ]]; then passed=false; status=1; fi
jq -n --argjson passed "$passed" '{passed: $passed, profile: {controlPath: "local", edgeAuthentication: false}}' >"$1/result.json"
exit "$status"
SCRIPT
chmod 0700 "${fixture}/runner"
for scenario in pass fail incomplete; do
  status=0
  CASE="${scenario}" EXPECTED_HOLD=1s RSTREAM_FORMAT_QUALIFICATION_RUNNER="${fixture}/runner" \
    "${script_directory}/compare-direct.sh" 1s "${fixture}/${scenario}" || status=$?
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
  "${script_directory}/compare-direct.sh" 3s "${fixture}/three-seconds"
if "${script_directory}/compare-direct.sh" invalid "${fixture}/invalid" >/dev/null 2>&1; then exit 1; fi
[[ ! -e "${fixture}/invalid" ]]
printf 'Direct format comparison fixtures passed\n'
