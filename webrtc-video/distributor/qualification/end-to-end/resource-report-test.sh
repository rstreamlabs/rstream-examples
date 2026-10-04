#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
fixture="$(mktemp "${TMPDIR:-/tmp}/rstream-resource-report.XXXXXX")"
trap 'rm -f "${fixture}"' EXIT INT TERM

cat >"${fixture}" <<'EOF'
{"Name":"producer","CPUPerc":"25.00%","MemUsage":"1MiB / 4GiB","NetIO":"1kB / 2kB","PIDs":"4"}
{"Name":"browser","CPUPerc":"100.00%","MemUsage":"0B / 0B","ResidentBytes":524288,"ResidentBytesSource":"process-pss","NetIO":"3MB / 4MB","PIDs":"8"}
{"Name":"producer","CPUPerc":"50.00%","MemUsage":"2MiB / 4GiB","NetIO":"2KiB / 3KiB","PIDs":"5"}
{"Name":"distributor","CPUPerc":"10.00%","MemUsage":"1.5MiB / 4GiB","NetIO":"1GiB / 1.25GiB","PIDs":"2"}
EOF

jq -s \
  --arg producer_name producer \
  --arg browser_name browser \
  --arg distributor_name distributor \
  -f "${script_directory}/resource-report.jq" \
  "${fixture}" | jq -e '
    .components.producer.samples == 2 and
    .components.producer.cpuCoreRatio.average == 0.375 and
    .components.producer.cpuCoreRatio.p95 == 0.5 and
    .components.producer.cpuCoreRatio.maximum == 0.5 and
    .components.producer.residentBytes.maximum == 2097152 and
    .components.producer.residentBytes.sources == ["container-cgroup"] and
    .components.producer.network.receivedBytes == 2048 and
    .components.producer.network.transmittedBytes == 3072 and
    .components.producer.tasks.maximum == 5 and
    .components.browser.residentBytes.maximum == 524288 and
    .components.browser.residentBytes.sources == ["process-pss"] and
    .components.distributor.residentBytes.maximum == 1572864 and
    .components.distributor.network.transmittedBytes == 1342177280 and
    .conservativePeak.cpuCoreRatio == 1.6 and
    .conservativePeak.residentBytes == 4194304 and
    .conservativePeak.tasks == 15 and
    .phases == {} and .samplesWithoutStablePhase == 4
  ' >/dev/null

jq -c 'if .Name == "producer" and .CPUPerc == "25.00%" then
  .samplePhase = {name: "warmup", startedAt: "2026-10-04T00:00:00Z"}
  elif .Name == "distributor" then .samplePhase = null
  else .samplePhase = {name: "baseline", startedAt: "2026-10-04T00:00:20Z"} end' \
  "${fixture}" >"${fixture}.phases"
trap 'rm -f "${fixture}" "${fixture}.phases"' EXIT INT TERM
jq -s --arg producer_name producer --arg browser_name browser --arg distributor_name distributor \
  -f "${script_directory}/resource-report.jq" "${fixture}.phases" | jq -e '
    .components.producer.samples == 2 and
    .phases.warmup.components.producer.cpuCoreRatio.average == 0.25 and
    .phases.baseline.components.producer.cpuCoreRatio.average == 0.5 and
    .phases.baseline.components.browser.samples == 1 and
    .phases.baseline.components.distributor == null and
    .phases.baseline.conservativePeak.cpuCoreRatio == 1.5 and
    .samplesWithoutStablePhase == 1
  ' >/dev/null

printf 'Resource report tests passed\n'
