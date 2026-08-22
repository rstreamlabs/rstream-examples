#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
platform_directory="$(cd "${script_directory}/../.." && pwd -P)"
video_directory="$(cd "${platform_directory}/.." && pwd -P)"
repository_directory="$(git -C "${video_directory}" rev-parse --show-toplevel)"
producer_directory="${video_directory}/producer"
output_directory="${1:-}"
exposure="${2:-public}"
postgres_image="postgres:17-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"

if [[ -z "${output_directory}" ]]; then
  printf 'usage: %s OUTPUT_DIRECTORY [public|rstream]\n' "$0" >&2
  exit 1
fi
case "${exposure}" in
public | rstream)
  ;;
*)
  printf 'exposure must be public or rstream\n' >&2
  exit 1
  ;;
esac
for command in curl docker git node npm; do
  if ! command -v "${command}" >/dev/null; then
    printf 'required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done
if [[ ! -s "${platform_directory}/.env.local" ]]; then
  printf 'platform/.env.local is required for the finite platform qualification\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

revision="$(git -C "${repository_directory}" rev-parse HEAD)"
revision_short="${revision:0:12}"
suffix="$(printf '%s-%s' "$$" "${RANDOM}")"
postgres_name="rstream-video-platform-postgres-${suffix}"
producer_name="rstream-video-platform-producer-${suffix}"
producer_image="rstream-webrtc-platform-producer:${revision_short}"
runtime_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-video-platform.XXXXXX")"
state_file="${runtime_directory}/stack.json"
stack_log="${runtime_directory}/stack.log"
producer_log="${runtime_directory}/producer.log"
mediamtx_log="${runtime_directory}/mediamtx.log"
postgres_log="${runtime_directory}/postgres.log"
stack_pid=""
mediamtx_container=""
postgres_started=0
producer_started=0

cleanup() {
  local status=$?
  if ((postgres_started)); then
    docker logs "${postgres_name}" >"${postgres_log}" 2>&1 || true
  fi
  if ((producer_started)); then
    docker logs "${producer_name}" >"${producer_log}" 2>&1 || true
    docker rm -f "${producer_name}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${mediamtx_container}" ]] && docker inspect "${mediamtx_container}" >/dev/null 2>&1; then
    docker logs "${mediamtx_container}" >"${mediamtx_log}" 2>&1 || true
  fi
  if [[ -n "${stack_pid}" ]]; then
    kill -TERM "${stack_pid}" >/dev/null 2>&1 || true
    wait "${stack_pid}" >/dev/null 2>&1 || true
  fi
  if ((postgres_started)); then
    docker rm -f "${postgres_name}" >/dev/null 2>&1 || true
  fi
  if [[ -s "${stack_log}" ]]; then
    node "${video_directory}/distributor/qualification/end-to-end/sanitize-stream.mjs" \
      <"${stack_log}" >"${output_directory}/stack.log" || status=1
  fi
  if [[ -s "${producer_log}" ]]; then
    node "${video_directory}/distributor/qualification/end-to-end/sanitize-stream.mjs" \
      <"${producer_log}" >"${output_directory}/producer.log" || status=1
  fi
  if [[ -s "${mediamtx_log}" ]]; then
    node "${video_directory}/distributor/qualification/end-to-end/sanitize-stream.mjs" \
      <"${mediamtx_log}" >"${output_directory}/mediamtx.log" || status=1
  fi
  if [[ -s "${postgres_log}" ]]; then
    node "${video_directory}/distributor/qualification/end-to-end/sanitize-stream.mjs" \
      <"${postgres_log}" >"${output_directory}/postgres.log" || status=1
  fi
  rm -rf "${runtime_directory}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

case "${RSTREAM_BROWSER_EXECUTABLE:-}" in
"")
  if [[ -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ]]; then
    browser_executable="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
  elif command -v chromium >/dev/null; then
    browser_executable="$(command -v chromium)"
  elif command -v chromium-browser >/dev/null; then
    browser_executable="$(command -v chromium-browser)"
  else
    printf 'Chrome or Chromium is required; set RSTREAM_BROWSER_EXECUTABLE\n' >&2
    exit 1
  fi
  ;;
*)
  browser_executable="${RSTREAM_BROWSER_EXECUTABLE}"
  ;;
esac
if [[ ! -x "${browser_executable}" ]]; then
  printf 'browser executable is not executable: %s\n' "${browser_executable}" >&2
  exit 1
fi

printf 'Starting an isolated PostgreSQL database\n'
docker run --detach \
  --name "${postgres_name}" \
  --env POSTGRES_DB=webrtc_video_platform \
  --env POSTGRES_PASSWORD=qualification \
  --env POSTGRES_USER=qualification \
  --publish 127.0.0.1::5432/tcp \
  --read-only \
  --security-opt no-new-privileges \
  --tmpfs /run/postgresql:rw,nosuid,size=16m \
  --tmpfs /tmp:rw,nosuid,size=16m \
  --tmpfs /var/lib/postgresql/data:rw,nosuid,size=512m \
  "${postgres_image}" >/dev/null
postgres_started=1
for _ in $(seq 1 60); do
  if docker exec "${postgres_name}" pg_isready -U qualification -d webrtc_video_platform >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
if ! docker exec "${postgres_name}" pg_isready -U qualification -d webrtc_video_platform >/dev/null 2>&1; then
  printf 'PostgreSQL did not become ready\n' >&2
  docker inspect --format 'state={{.State.Status}} exit={{.State.ExitCode}} error={{.State.Error}} oom={{.State.OOMKilled}}' \
    "${postgres_name}" >&2 || true
  docker logs "${postgres_name}" >&2 || true
  exit 1
fi
postgres_port="$(docker port "${postgres_name}" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
if ! [[ "${postgres_port}" =~ ^[0-9]+$ ]]; then
  printf 'could not resolve the isolated PostgreSQL port\n' >&2
  exit 1
fi
database_url="postgresql://qualification:qualification@127.0.0.1:${postgres_port}/webrtc_video_platform?schema=public"
POSTGRES_PRISMA_DIRECT_URL="${database_url}" \
  POSTGRES_PRISMA_POOL_URL="${database_url}" \
  npm --prefix "${platform_directory}" run prisma:deploy >/dev/null

user_id="qualification-user-${suffix}"
device_id="$(node -e 'process.stdout.write(require("node:crypto").randomUUID())')"
device_secret="dev_$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64url"))')"
device_secret_hash="$(node -e 'process.stdout.write(require("node:crypto").createHash("sha256").update(process.argv[1]).digest("hex"))' "${device_secret}")"
device_secret_prefix="${device_secret:0:12}"
session_token="$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64url"))')"
tunnel_name="device-${device_id}"
docker exec --interactive "${postgres_name}" psql -v ON_ERROR_STOP=1 -U qualification -d webrtc_video_platform >/dev/null <<SQL
INSERT INTO users (id, name, email, "updatedAt")
VALUES ('${user_id}', 'Qualification user', '${user_id}@example.invalid', CURRENT_TIMESTAMP);
INSERT INTO sessions ("sessionToken", "userId", expires, "updatedAt")
VALUES ('${session_token}', '${user_id}', CURRENT_TIMESTAMP + INTERVAL '1 hour', CURRENT_TIMESTAMP);
INSERT INTO devices (id, "userId", name, "secretHash", "secretPrefix", "tunnelName", "updatedAt")
VALUES ('${device_id}', '${user_id}', 'Qualification camera', '${device_secret_hash}', '${device_secret_prefix}', '${tunnel_name}', CURRENT_TIMESTAMP);
SQL

printf 'Starting Next.js and the MediaMTX adapter stack\n'
POSTGRES_PRISMA_DIRECT_URL="${database_url}" \
  POSTGRES_PRISMA_POOL_URL="${database_url}" \
  "${platform_directory}/scripts/run-local-mediamtx.mjs" \
    --exposure "${exposure}" \
    --next-mode production \
    --state-file "${state_file}" >"${stack_log}" 2>&1 &
stack_pid=$!
for _ in $(seq 1 240); do
  if [[ -s "${state_file}" ]]; then
    break
  fi
  if ! kill -0 "${stack_pid}" 2>/dev/null; then
    printf 'the local MediaMTX stack exited before becoming ready\n' >&2
    tail -n 80 "${stack_log}" >&2
    exit 1
  fi
  sleep 0.5
done
if [[ ! -s "${state_file}" ]]; then
  printf 'the local MediaMTX stack did not become ready\n' >&2
  exit 1
fi
platform="$(node -e 'const value=require(process.argv[1]); if (!value.ready) process.exit(1); process.stdout.write(value.platform)' "${state_file}")"
platform_callback="$(node -e 'const value=require(process.argv[1]); if (!value.ready) process.exit(1); process.stdout.write(value.platformCallback)' "${state_file}")"
mediamtx_container="$(node -e 'const value=require(process.argv[1]); if (!value.ready) process.exit(1); process.stdout.write(value.containerName)' "${state_file}")"

printf 'Starting the unchanged producer through platform provisioning\n'
docker build --provenance=false \
  --file "${producer_directory}/qualification/adaptive-streaming/Dockerfile" \
  --tag "${producer_image}" \
  "${video_directory}" >/dev/null
docker run --detach \
  --name "${producer_name}" \
  --read-only \
  --security-opt no-new-privileges \
  --cap-drop ALL \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --env "API_URL=${platform_callback}" \
  --env "DEVICE_SECRET=${device_secret}" \
  --mount "type=bind,source=${producer_directory}/config.provisioning.h264.yaml,target=/qualification/config.yaml,readonly" \
  "${producer_image}" -config /qualification/config.yaml >/dev/null
producer_started=1

printf 'Running browser playback, fallback, and recovery gates\n'
RSTREAM_QUALIFICATION_SESSION_TOKEN="${session_token}" \
  node "${script_directory}/browser.mjs" \
  --platform "${platform}" \
  --container "${mediamtx_container}" \
  --browser-executable "${browser_executable}" \
  --output "${output_directory}/browser.json"

if [[ "$(node -p 'require(process.argv[1]).passed' "${output_directory}/browser.json")" != true ]]; then
  printf 'browser qualification did not pass\n' >&2
  exit 1
fi
working_tree_dirty=false
if [[ -n "$(git -C "${repository_directory}" status --porcelain)" ]]; then
  working_tree_dirty=true
fi
node - "${output_directory}/browser.json" "${output_directory}/summary.json" "${revision}" "${working_tree_dirty}" "${exposure}" <<'NODE'
const { readFileSync, writeFileSync } = require("node:fs")
const [browserPath, summaryPath, revision, workingTreeDirty, exposure] = process.argv.slice(2)
const browser = JSON.parse(readFileSync(browserPath, "utf8"))
const clean = workingTreeDirty !== "true"
writeFileSync(summaryPath, `${JSON.stringify({
  browser,
  exposure,
  passed: browser.passed === true && clean,
  revision,
  version: 1,
  workingTreeDirty: workingTreeDirty === "true",
}, null, 2)}\n`, { mode: 0o600 })
if (!clean) process.exitCode = 1
NODE
printf 'Platform qualification passed: %s\n' "${output_directory}/summary.json"
