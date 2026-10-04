#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=phase.sh
# shellcheck disable=SC1091
source "${script_directory}/phase.sh"
image="${1:?usage: phase-container-test.sh EXISTING_BROWSER_IMAGE}"
fixture_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-container-phase.XXXXXX")"
container="rstream-phase-test-$$-${RANDOM}"
cleanup() {
  docker rm -f "${container}" >/dev/null 2>&1 || true
  rm -rf "${fixture_directory}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

phase_file="${fixture_directory}/phase.json"
write_phase_file "${phase_file}" initial
# The reader deliberately does not retry missing/partial documents. No live
# media runs here; read concurrently with repeated atomic publications.
cat >"${fixture_directory}/reader.cjs" <<'JS'
const fs = require("node:fs");
let reads = 0;
let finalName = "";
const timeout = setTimeout(() => { throw new Error("phase test timed out"); }, 30000);
const interval = setInterval(() => {
  const phase = JSON.parse(fs.readFileSync("/tmp/phase.json", "utf8"));
  if (typeof phase.name !== "string" || !Number.isFinite(Date.parse(phase.startedAt))) {
    throw new Error("invalid phase");
  }
  reads++;
  finalName = phase.name;
  if (fs.existsSync("/tmp/stop")) {
    clearInterval(interval);
    clearTimeout(timeout);
    if (reads < 10 || finalName !== "phase-100") throw new Error("incomplete phase publication");
    if (fs.readdirSync("/tmp").some(name => name.startsWith(".phase.json.tmp."))) {
      throw new Error("temporary phase document leaked");
    }
    console.log(JSON.stringify({ reads, finalName }));
  }
}, 1);
JS

docker run --detach --name "${container}" --network none \
  --user "$(id -u):$(id -g)" --read-only --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,size=4m \
  --mount "type=bind,source=${fixture_directory},target=/bootstrap,readonly" \
  --entrypoint /bin/sh "${image}" -ceu \
  'cp /bootstrap/phase.json /tmp/phase.json; exec node /bootstrap/reader.cjs' >/dev/null
# Verify bootstrap completion before removing the original document.
for _ in $(seq 1 100); do
  if docker exec "${container}" test -s /tmp/phase.json; then break; fi
  sleep 0.01
done
docker exec "${container}" test -s /tmp/phase.json
rm "${phase_file}"
for iteration in $(seq 1 100); do
  write_phase_file "${phase_file}" "phase-${iteration}"
  copy_phase_to_container "${container}" "${phase_file}" /tmp/phase.json
  rm "${phase_file}"
done
docker exec "${container}" touch /tmp/stop
container_status="$(docker wait "${container}")"
docker logs "${container}"
[[ "${container_status}" == 0 ]]
