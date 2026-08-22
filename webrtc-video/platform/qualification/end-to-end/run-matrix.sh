#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_directory="${1:-${script_directory}/../../.artifacts/distribution-${timestamp}}"
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

for exposure in public rstream; do
  "${script_directory}/run.sh" "${output_directory}/${exposure}" "${exposure}"
done
node - "${output_directory}" <<'NODE'
const { readFileSync, writeFileSync } = require("node:fs")
const { join } = require("node:path")
const directory = process.argv[2]
const runs = ["public", "rstream"].map((exposure) =>
  JSON.parse(readFileSync(join(directory, exposure, "summary.json"), "utf8")),
)
const summary = {
  gates: {
    publicExposure: runs[0].passed === true,
    rstreamExposure: runs[1].passed === true,
  },
  passed: runs.every((run) => run.passed === true),
  runs,
  version: 1,
}
writeFileSync(join(directory, "summary.json"), `${JSON.stringify(summary, null, 2)}\n`, { mode: 0o600 })
if (!summary.passed) process.exitCode = 1
NODE
printf 'Platform exposure matrix passed: %s\n' "${output_directory}/summary.json"
