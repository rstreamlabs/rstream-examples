#!/usr/bin/env bash

set -Eeuo pipefail

interrupted=0
scenario_pid=""
handle_signal() {
	interrupted=1
	if [[ -n "${scenario_pid}" ]] && kill -0 "${scenario_pid}" 2>/dev/null; then
		kill -TERM "${scenario_pid}" 2>/dev/null || true
	fi
}
trap handle_signal INT TERM

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
"${script_directory}/check.sh"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_directory="${1:-${script_directory}/.artifacts/${timestamp}}"
if [[ -e "${output_directory}" ]] &&
  [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

scenarios=(
  reliable-media-matrix
  datagram-media
  datagram-media-guaranteed
  rtcp-repair
  rtsp-media-matrix
)
failures=0
for scenario in "${scenarios[@]}"; do
  printf 'Running %s\n' "${scenario}"
  scenario_status=0
	bash "${script_directory}/${scenario}.sh" \
		"${output_directory}/${scenario}" \
		> >(tee "${output_directory}/${scenario}.log") \
		2> >(tee "${output_directory}/${scenario}.stderr" >&2) &
	scenario_pid=$!
	wait "${scenario_pid}" || scenario_status=$?
	scenario_pid=""
	if ((interrupted == 1)); then
		exit 130
	fi
  printf '%s\n' "${scenario_status}" >"${output_directory}/${scenario}.status"
  if ((scenario_status != 0)); then
    failures=$((failures + 1))
	fi
done

scenario=log-quality
scenarios+=("${scenario}")
mkdir -p "${output_directory}/${scenario}"
scenario_status=0
python3 "${script_directory}/analyze_logs.py" \
	--root "${output_directory}" \
	--output "${output_directory}/${scenario}/analysis.json" \
	--summary "${output_directory}/${scenario}/summary.txt" \
	>"${output_directory}/${scenario}.log" \
	2>"${output_directory}/${scenario}.stderr" || scenario_status=$?
printf '%s\n' "${scenario_status}" >"${output_directory}/${scenario}.status"
if ((scenario_status != 0)); then
	failures=$((failures + 1))
fi

python3 - "${output_directory}" "${failures}" "${scenarios[@]}" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

root = pathlib.Path(sys.argv[1])
failure_count = int(sys.argv[2])
scenario_names = sys.argv[3:]
scenarios = []
for name in scenario_names:
    status = int((root / f"{name}.status").read_text().strip())
    summary_path = root / name / "summary.txt"
    scenarios.append(
        {
            "name": name,
            "passed": status == 0,
            "exitStatus": status,
            "summary": summary_path.read_text().strip()
            if summary_path.exists()
            else "no scenario summary was produced",
        }
    )

summary = {
    "generatedAt": datetime.now(timezone.utc).isoformat(),
    "passed": failure_count == 0,
    "scenarios": scenarios,
}
(root / "summary.json").write_text(
    json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
lines = [
    f"# Netcat media qualification — {'PASS' if summary['passed'] else 'FAIL'}",
    "",
    "| Scenario | Verdict | Result |",
    "| --- | --- | --- |",
]
for scenario in scenarios:
    result = scenario["summary"].replace("\n", " ").replace("|", "\\|")
    lines.append(
        f"| {scenario['name']} | {'PASS' if scenario['passed'] else 'FAIL'} | {result} |"
    )
lines.extend(
    [
        "",
        "Each scenario directory contains its pinned manifest, process logs, exact frame-count evidence, and raw decoded buffers.",
        "The result qualifies only the recorded repository revision, binaries, environment, and parameters.",
        "",
    ]
)
(root / "summary.md").write_text("\n".join(lines), encoding="utf-8")
PY

python3 "${script_directory}/render_report.py" "${output_directory}"

if find "${output_directory}" -type f ! -name '*.i420' -print0 |
  xargs -0 grep -El 'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}' \
    >"${output_directory}/secret-scan.matches"; then
  printf 'refusing qualification artifacts that appear to contain a token\n' >&2
  exit 1
fi
find "${output_directory}" -name 'secret-scan.matches' -size 0 -delete

if find "${output_directory}" -type f ! -name '*.i420' ! -name '*.h264' \
  -print0 | xargs -0 grep -EIl \
  '(/Users/[^/]+|/home/[^/]+|[A-Za-z]:\\Users\\[^\\]+|Darwin [^ ]+\.local)' \
  >"${output_directory}/personal-data-scan.matches"; then
  printf 'refusing qualification artifacts that expose a local user profile\n' >&2
  exit 1
fi
find "${output_directory}" -name 'personal-data-scan.matches' -size 0 -delete

if ((failures > 0)); then
  printf 'FAIL: %s/%s netcat media scenarios failed; see %s/summary.md\n' \
    "${failures}" "${#scenarios[@]}" "${output_directory}" >&2
  exit 1
fi
printf 'PASS: %s\n' "${output_directory}/summary.md"
