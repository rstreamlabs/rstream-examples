#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
uses_mediamtx=false
uses_adapter=false
# shellcheck source=mode.sh
# shellcheck disable=SC1091
source "${script_directory}/mode.sh"
# shellcheck source=phase.sh
# shellcheck disable=SC1091
source "${script_directory}/phase.sh"
distributor_directory="$(cd "${script_directory}/../.." && pwd -P)"
video_directory="$(cd "${distributor_directory}/.." && pwd -P)"
producer_directory="${video_directory}/producer"
qualification_directory="${producer_directory}/qualification/adaptive-streaming"
repository_directory="$(git -C "${video_directory}" rev-parse --show-toplevel)"
context_name="${RSTREAM_CONTEXT:-}"
rstream_cli="${RSTREAM_CLI:-rstream}"
distribution_mode="${RSTREAM_DISTRIBUTOR_MODE:-mediamtx}"
warmup_seconds="${RSTREAM_DISTRIBUTOR_WARMUP_SECONDS:-20}"
duration_seconds="${RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS:-15}"
edge_auth="${RSTREAM_DISTRIBUTOR_EDGE_AUTH:-true}"
recovery_seconds="${RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS:-45}"
flexfec_media_packets="${RSTREAM_DISTRIBUTOR_FLEXFEC_MEDIA_PACKETS:-5}"
flexfec_repair_packets="${RSTREAM_DISTRIBUTOR_FLEXFEC_REPAIR_PACKETS:-1}"
viewer_loss_percent="${RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT:-0}"
viewer_capacity_kbps="${RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS:-0}"
viewer_delay_milliseconds="${RSTREAM_DISTRIBUTOR_VIEWER_DELAY_MILLISECONDS:-0}"
viewer_jitter_milliseconds="${RSTREAM_DISTRIBUTOR_VIEWER_JITTER_MILLISECONDS:-0}"
viewer_queue_packets="${RSTREAM_DISTRIBUTOR_VIEWER_QUEUE_PACKETS:-256}"
source_loss_percent="${RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT:-0}"
source_capacity_kbps="${RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS:-0}"
source_delay_milliseconds="${RSTREAM_DISTRIBUTOR_SOURCE_DELAY_MILLISECONDS:-0}"
source_jitter_milliseconds="${RSTREAM_DISTRIBUTOR_SOURCE_JITTER_MILLISECONDS:-0}"
source_queue_packets="${RSTREAM_DISTRIBUTOR_SOURCE_QUEUE_PACKETS:-256}"
playout_delay_hint_seconds="${RSTREAM_DISTRIBUTOR_PLAYOUT_DELAY_HINT_SECONDS:-0}"
output_directory="${1:-}"

if [[ -z "${context_name}" ]]; then
  printf 'RSTREAM_CONTEXT must name an explicit qualification context\n' >&2
  exit 1
fi
if [[ -z "${output_directory}" ]]; then
  printf 'usage: RSTREAM_CONTEXT=<context> %s OUTPUT_DIRECTORY\n' "$0" >&2
  exit 1
fi
if ! select_distribution_mode "${distribution_mode}"; then
  printf 'RSTREAM_DISTRIBUTOR_MODE must be direct, mediamtx, or mediamtx-native\n' >&2
  exit 1
fi
case "${edge_auth}" in
true | false)
  ;;
*)
  printf 'RSTREAM_DISTRIBUTOR_EDGE_AUTH must be true or false\n' >&2
  exit 1
  ;;
esac
if ! [[ "${duration_seconds}" =~ ^[0-9]+$ ]] || ((duration_seconds < 10 || duration_seconds > 300)); then
  printf 'RSTREAM_DISTRIBUTOR_QUALIFICATION_SECONDS must be from 10 through 300\n' >&2
  exit 1
fi
if ! [[ "${warmup_seconds}" =~ ^[0-9]+$ ]] || ((warmup_seconds < 10 || warmup_seconds > 300)); then
  printf 'RSTREAM_DISTRIBUTOR_WARMUP_SECONDS must be from 10 through 300\n' >&2
  exit 1
fi
if ! [[ "${recovery_seconds}" =~ ^[0-9]+$ ]] || ((recovery_seconds < 15 || recovery_seconds > 300)); then
  printf 'RSTREAM_DISTRIBUTOR_RECOVERY_SECONDS must be from 15 through 300\n' >&2
  exit 1
fi
if ! [[ "${flexfec_media_packets}" =~ ^[0-9]+$ ]] ||
  ! [[ "${flexfec_repair_packets}" =~ ^[0-9]+$ ]]; then
  printf 'RSTREAM_DISTRIBUTOR_FLEXFEC_MEDIA_PACKETS and RSTREAM_DISTRIBUTOR_FLEXFEC_REPAIR_PACKETS must be positive integers\n' >&2
  exit 1
fi
flexfec_media_packets=$((10#${flexfec_media_packets}))
flexfec_repair_packets=$((10#${flexfec_repair_packets}))
if ((flexfec_media_packets < 1 || flexfec_media_packets > 110)); then
  printf 'RSTREAM_DISTRIBUTOR_FLEXFEC_MEDIA_PACKETS must be from 1 through 110\n' >&2
  exit 1
fi
if ((flexfec_repair_packets < 1 || flexfec_repair_packets > flexfec_media_packets)); then
  printf 'RSTREAM_DISTRIBUTOR_FLEXFEC_REPAIR_PACKETS must be from 1 through the media-packet count\n' >&2
  exit 1
fi
for command in docker git go jq node; do
  if ! command -v "${command}" >/dev/null; then
    printf 'required command not found: %s\n' "${command}" >&2
    exit 1
  fi
done
if ! command -v "${rstream_cli}" >/dev/null; then
  printf 'required command not found: %s\n' "${rstream_cli}" >&2
  exit 1
fi
if ! "${rstream_cli}" token create --help 2>&1 | grep -q -- '--expires-in'; then
  printf 'the selected rstream CLI cannot create bounded-lifetime tokens; install version 1.29.0 or newer, or set RSTREAM_CLI to a compatible binary\n' >&2
  exit 1
fi
if ! jq -en --arg value "${viewer_loss_percent}" '
  ($value | tonumber) as $loss | $loss >= 0 and $loss <= 20
' >/dev/null 2>&1; then
  printf 'RSTREAM_DISTRIBUTOR_VIEWER_LOSS_PERCENT must be from 0 through 20\n' >&2
  exit 1
fi
viewer_loss_percent="$(jq -nr --arg value "${viewer_loss_percent}" '$value | tonumber')"
if ! jq -en --arg value "${playout_delay_hint_seconds}" '
  ($value | tonumber) as $seconds | $seconds >= 0 and $seconds <= 1
' >/dev/null 2>&1; then
  printf 'RSTREAM_DISTRIBUTOR_PLAYOUT_DELAY_HINT_SECONDS must be from 0 through 1\n' >&2
  exit 1
fi
playout_delay_hint_seconds="$(jq -nr --arg value "${playout_delay_hint_seconds}" '$value | tonumber')"
for value in "${viewer_capacity_kbps}" "${viewer_delay_milliseconds}" "${viewer_jitter_milliseconds}" "${viewer_queue_packets}"; do
  if ! [[ "${value}" =~ ^[0-9]+$ ]]; then
    printf 'viewer capacity, delay, jitter, and queue values must be non-negative integers\n' >&2
    exit 1
  fi
done
viewer_capacity_kbps=$((10#${viewer_capacity_kbps}))
viewer_delay_milliseconds=$((10#${viewer_delay_milliseconds}))
viewer_jitter_milliseconds=$((10#${viewer_jitter_milliseconds}))
viewer_queue_packets=$((10#${viewer_queue_packets}))
if ((viewer_capacity_kbps != 0 && viewer_capacity_kbps < 100)); then
  printf 'RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS must be zero or at least 100\n' >&2
  exit 1
fi
if ((viewer_delay_milliseconds > 2000 || viewer_jitter_milliseconds > 1000 || viewer_jitter_milliseconds > viewer_delay_milliseconds)); then
  printf 'viewer delay must be at most 2000 ms and jitter must not exceed the delay or 1000 ms\n' >&2
  exit 1
fi
if ((viewer_queue_packets < 32 || viewer_queue_packets > 4096)); then
  printf 'RSTREAM_DISTRIBUTOR_VIEWER_QUEUE_PACKETS must be from 32 through 4096\n' >&2
  exit 1
fi
if ! jq -en --arg value "${source_loss_percent}" '
  ($value | tonumber) as $loss | $loss >= 0 and $loss <= 20
' >/dev/null 2>&1; then
  printf 'RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT must be from 0 through 20\n' >&2
  exit 1
fi
source_loss_percent="$(jq -nr --arg value "${source_loss_percent}" '$value | tonumber')"
for value in "${source_capacity_kbps}" "${source_delay_milliseconds}" "${source_jitter_milliseconds}" "${source_queue_packets}"; do
  if ! [[ "${value}" =~ ^[0-9]+$ ]]; then
    printf 'source capacity, delay, jitter, and queue values must be non-negative integers\n' >&2
    exit 1
  fi
done
source_capacity_kbps=$((10#${source_capacity_kbps}))
source_delay_milliseconds=$((10#${source_delay_milliseconds}))
source_jitter_milliseconds=$((10#${source_jitter_milliseconds}))
source_queue_packets=$((10#${source_queue_packets}))
if ((source_capacity_kbps != 0 && source_capacity_kbps < 100)); then
  printf 'RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS must be zero or at least 100\n' >&2
  exit 1
fi
if ((source_delay_milliseconds > 2000 || source_jitter_milliseconds > 1000 || source_jitter_milliseconds > source_delay_milliseconds)); then
  printf 'source delay must be at most 2000 ms and jitter must not exceed the delay or 1000 ms\n' >&2
  exit 1
fi
if ((source_queue_packets < 32 || source_queue_packets > 4096)); then
  printf 'RSTREAM_DISTRIBUTOR_SOURCE_QUEUE_PACKETS must be from 32 through 4096\n' >&2
  exit 1
fi
viewer_network_enabled="$(jq -nr \
  --argjson loss "${viewer_loss_percent}" \
  --argjson capacity "${viewer_capacity_kbps}" \
  --argjson delay "${viewer_delay_milliseconds}" \
  --argjson jitter "${viewer_jitter_milliseconds}" \
  '$loss > 0 or $capacity > 0 or $delay > 0 or $jitter > 0')"
source_network_enabled="$(jq -nr \
  --argjson loss "${source_loss_percent}" \
  --argjson capacity "${source_capacity_kbps}" \
  --argjson delay "${source_delay_milliseconds}" \
  --argjson jitter "${source_jitter_milliseconds}" \
  '$loss > 0 or $capacity > 0 or $delay > 0 or $jitter > 0')"
if [[ "${viewer_network_enabled}" == true && "${source_network_enabled}" == true ]]; then
  printf 'source and viewer network impairment cannot be enabled in the same causal qualification run\n' >&2
  exit 1
fi
if [[ "${source_network_enabled}" == true && "${uses_adapter}" != true ]]; then
  printf 'source network impairment requires RSTREAM_DISTRIBUTOR_MODE=mediamtx\n' >&2
  exit 1
fi
collector_maximum_duration_seconds=$((warmup_seconds + duration_seconds + 60))
if [[ "${viewer_network_enabled}" == true || "${source_network_enabled}" == true ]]; then
  collector_maximum_duration_seconds=$((warmup_seconds + duration_seconds * 2 + recovery_seconds + 60))
fi
connect_token_ttl_seconds=$((collector_maximum_duration_seconds + 180))
if ((connect_token_ttl_seconds < 300)); then
  connect_token_ttl_seconds=300
fi
qualification_token_ttl_seconds=$((collector_maximum_duration_seconds + 600))
if ((qualification_token_ttl_seconds < 900)); then
  qualification_token_ttl_seconds=900
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

revision="$(git -C "${repository_directory}" rev-parse HEAD)"
revision_short="${revision:0:12}"
working_tree_dirty=false
if [[ -n "$(git -C "${repository_directory}" status --porcelain)" ]]; then
  working_tree_dirty=true
fi
producer_image="rstream-webrtc-distribution-producer:${revision_short}"
browser_image="rstream-webrtc-distribution-browser:${revision_short}"
distributor_image="rstream-video-distributor:${revision_short}"
suffix="$$-${RANDOM}"
network_name="rstream-distribution-qualification-${suffix}"
producer_name="rstream-distribution-producer-${suffix}"
distributor_name="rstream-distribution-mediamtx-${suffix}"
browser_name="rstream-distribution-browser-${suffix}"
runtime_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-distribution-qualification.XXXXXX")"
control_directory="${runtime_directory}/control"
container_user="$(id -u):$(id -g)"
producer_started=0
distributor_started=0
browser_started=0
network_created=0
resource_sampler_pid=0

capture_logs() {
  if ((producer_started)); then
    docker logs "${producer_name}" 2>&1 | node "${script_directory}/sanitize-stream.mjs" >"${output_directory}/producer.log" || true
  fi
  if ((distributor_started)); then
    docker logs "${distributor_name}" 2>&1 | node "${script_directory}/sanitize-stream.mjs" >"${output_directory}/distributor.log" || true
  fi
  if ((browser_started)); then
    docker logs "${browser_name}" 2>&1 | node "${script_directory}/sanitize-stream.mjs" >"${output_directory}/browser.log" || true
  fi
}

stop_resource_sampler() {
  if ((resource_sampler_pid)); then
    kill -TERM "${resource_sampler_pid}" >/dev/null 2>&1 || true
    wait "${resource_sampler_pid}" >/dev/null 2>&1 || true
    resource_sampler_pid=0
  fi
}

network_helper() {
  docker run --rm \
    --network "container:${impairment_target_name}" \
    --user 0 \
    --read-only \
    --security-opt no-new-privileges \
    --cap-add NET_ADMIN \
    --env "DESTINATION_IP=${impairment_destination_ip}" \
    --env "DESTINATION_PORT=${impairment_destination_port:-}" \
    --env "LOSS_PERCENT=${impairment_loss_percent}" \
    --env "CAPACITY_KBPS=${impairment_capacity_kbps}" \
    --env "DELAY_MILLISECONDS=${impairment_delay_milliseconds}" \
    --env "JITTER_MILLISECONDS=${impairment_jitter_milliseconds}" \
    --env "QUEUE_PACKETS=${impairment_queue_packets}" \
    --entrypoint /bin/sh \
    "${producer_image}" -ceu "$1"
}

process_resident_sample() {
  local container_name=$1
  local pid
  local process_lines
  local proportional_kibibytes
  local resident_kibibytes
  local rss_kibibytes
  local source=process-pss
  resident_kibibytes=0
  if ! process_lines="$(docker top "${container_name}" -eo pid,rss 2>/dev/null)"; then
    printf '0 process-unavailable'
    return
  fi
  while read -r pid rss_kibibytes; do
    if ! [[ "${pid}" =~ ^[0-9]+$ ]] || ! [[ "${rss_kibibytes}" =~ ^[0-9]+$ ]]; then
      continue
    fi
    proportional_kibibytes=
    if ! proportional_kibibytes="$(awk '$1 == "Pss:" && $2 ~ /^[0-9]+$/ && $3 == "kB" {print $2; exit}' "/proc/${pid}/smaps_rollup" 2>/dev/null)" \
      || ! [[ "${proportional_kibibytes}" =~ ^[0-9]+$ ]]; then
      proportional_kibibytes=
    fi
    if [[ -z "${proportional_kibibytes}" ]]; then
      proportional_kibibytes=${rss_kibibytes}
      source=process-pss-rss-fallback
    fi
    resident_kibibytes=$((resident_kibibytes + proportional_kibibytes))
  done <<<"${process_lines}"
  printf '%s %s' "$((resident_kibibytes * 1024))" "${source}"
}

sample_container_resources() {
  local container_name
  local resident_bytes
  local resident_bytes_source
  local sample
  local samples
  if ! samples="$(docker stats --no-stream --format '{{json .}}' "${resource_container_names[@]}")"; then
    return 1
  fi
  while IFS= read -r sample; do
    if [[ -z "${sample}" ]]; then
      continue
    fi
    if [[ "$(jq -r '(.MemUsage | split(" / ")[1]) == "0B"' <<<"${sample}")" == true ]]; then
      container_name="$(jq -er '.Name' <<<"${sample}")"
      read -r resident_bytes resident_bytes_source < <(process_resident_sample "${container_name}")
      jq -c --argjson resident_bytes "${resident_bytes}" --arg resident_bytes_source "${resident_bytes_source}" \
        '. + {ResidentBytes: $resident_bytes, ResidentBytesSource: $resident_bytes_source}' <<<"${sample}"
    else
      jq -c '. + {ResidentBytesSource: "container-cgroup"}' <<<"${sample}"
    fi
  done <<<"${samples}"
}

cleanup() {
  local status=$?
  stop_resource_sampler
  capture_logs
  if ((browser_started)); then
    docker rm -f "${browser_name}" >/dev/null 2>&1 || true
  fi
  if ((distributor_started)); then
    docker rm -f "${distributor_name}" >/dev/null 2>&1 || true
  fi
  if ((producer_started)); then
    docker rm -f "${producer_name}" >/dev/null 2>&1 || true
  fi
  if ((network_created)); then
    docker network rm "${network_name}" >/dev/null 2>&1 || true
  fi
  node "${script_directory}/sanitize-artifacts.mjs" "${output_directory}" >/dev/null 2>&1 || status=1
  rm -rf "${runtime_directory}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

write_phase() {
  write_phase_file "${control_directory}/phase.json" "$1"
}

printf 'Preparing an isolated rstream runtime\n'
project_endpoint="$(
  "${rstream_cli}" context list --output json |
    jq -er --arg context "${context_name}" '
      [.[] | select(.Name == $context)] |
      if length == 1 then .[0].ProjectEndpoint else error("qualification context is not unique") end
    '
)"
project_id="$(
  "${rstream_cli}" --context "${context_name}" project list --output json |
    jq -er --arg endpoint "${project_endpoint}" '
      [.projects[] | select(.endpoint == $endpoint)] |
      if length == 1 then .[0].id else error("qualification project is not unique") end
    '
)"
qualification_resources="$(
  jq -cn --arg project "${project_id}" --argjson token_auth "${edge_auth}" '{
    tunnels: {projects: [$project], scopes: {tunnels: {create: {filters: {
      name: {exact: "webrtc-video-producer-adaptive"},
      protocol: "http",
      publish: true,
      token_auth: $token_auth
    }}}}}
  }'
)"
qualification_token="$(
  "${rstream_cli}" --context "${context_name}" token create \
    --expires-in "${qualification_token_ttl_seconds}" \
    --resources-json "${qualification_resources}" \
    --output json |
    jq -er '.token | select(type == "string" and length > 0)'
)"
RSTREAM_AUTHENTICATION_TOKEN="${qualification_token}" go -C "${producer_directory}" run ./qualification/adaptive-streaming/cmd/prepare-context \
  -context "${context_name}" \
  -allow-mediamtx-native-offer="$([[ "${distribution_mode}" == mediamtx-native ]] && printf true || printf false)" \
  -embedded-viewer=false \
  -flex-fec=true \
  -flex-fec-media-packets "${flexfec_media_packets}" \
  -flex-fec-repair-packets "${flexfec_repair_packets}" \
  -producer-config "${producer_directory}/config.test-pattern.h264.twcc-gcc-flexfec.yaml" \
  -producer-turn-policy disabled \
  -tunnel-token-auth="${edge_auth}" \
  -output-directory "${runtime_directory}"
unset qualification_token
mkdir -m 0700 "${control_directory}"
write_phase warmup
jq -n '{enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 0, qdisc: null, filters: []}' \
  >"${output_directory}/viewer-network.json"
jq -n '{enabled: false, capacityKbps: 0, delayMilliseconds: 0, jitterMilliseconds: 0, lossPercent: 0, queuePackets: 0, qdisc: null, filters: []}' \
  >"${output_directory}/source-network.json"
jq -n '{}' >"${output_directory}/adapter-result.json"
jq -n '{fatalErrors: 0, h264PacketizationErrors: 0, packetLossWarnings: 0, transportBufferWarnings: 0}' \
  >"${output_directory}/runtime-health.json"
jq -n '{required: false}' >"${output_directory}/native-source-profile.json"

printf 'Building producer and browser images\n'
docker build --file "${qualification_directory}/Dockerfile" --tag "${producer_image}" "${video_directory}"
docker build --file "${qualification_directory}/Browser.Dockerfile" --tag "${browser_image}" "${video_directory}"
if [[ "${uses_mediamtx}" == true ]]; then
  printf 'Building the distributor image\n'
  docker build --file "${distributor_directory}/Dockerfile" --tag "${distributor_image}" "${distributor_directory}"
fi

docker network create --driver bridge "${network_name}" >/dev/null
network_created=1
docker run --detach \
  --name "${producer_name}" \
  --network "${network_name}" \
  --network-alias producer \
  --user "${container_user}" \
  --read-only \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --env HOME=/tmp \
  --env-file "${runtime_directory}/runtime.env" \
  --env RSTREAM_CONFIG=/runtime/config.yaml \
  --env RSTREAM_CONTEXT=qualification \
  --mount "type=bind,source=${runtime_directory}/config.yaml,target=/runtime/config.yaml,readonly" \
  --mount "type=bind,source=${runtime_directory}/relay-config.yaml,target=/runtime/producer.yaml,readonly" \
  "${producer_image}" -config /runtime/producer.yaml >/dev/null
producer_started=1

source_base=""
for _ in $(seq 1 90); do
  if [[ "$(docker inspect --format '{{.State.Running}}' "${producer_name}")" != true ]]; then
    printf 'producer exited before publishing its tunnel\n' >&2
    exit 1
  fi
  source_base="$(docker logs "${producer_name}" 2>&1 | sed -nE 's/.*Public URL: (https:\/\/[^[:space:]]+).*/\1/p' | tail -1)"
  if [[ -n "${source_base}" ]]; then
    break
  fi
  sleep 1
done
if [[ -z "${source_base}" ]]; then
  printf 'producer did not publish its tunnel within 90 seconds\n' >&2
  exit 1
fi

source_endpoint="${source_base%/}/whep"
if [[ "${edge_auth}" == true ]]; then
  source_authority="${source_base#*://}"
  source_authority="${source_authority%%/*}"
  tunnel_id="$(
    "${rstream_cli}" --context "${context_name}" tunnel list \
      --filter 'name=webrtc-video-producer-adaptive,status=online' \
      --output json |
      jq -er --arg authority "${source_authority}" '
        [.[] | select((.host // .hostname // "") == $authority)] |
        if length == 1 then .[0].id else error("temporary producer tunnel is not unique") end
      '
  )"
  connect_resources="$(
    jq -cn --arg id "${tunnel_id}" --arg project "${project_id}" '{
      tunnels: {projects: [$project], scopes: {tunnels: {connect: {
        filters: {
          id: $id,
          protocol: "http",
          publish: true,
          status: "online",
          token_auth: true
        },
        params: {path: {regex: "^/whep(?:/[^/?#]{1,256})?$"}}
      }}}}
    }'
  )"
  connect_token="$(
    "${rstream_cli}" --context "${context_name}" token create \
      --expires-in "${connect_token_ttl_seconds}" \
      --resources-json "${connect_resources}" \
      --output json |
      jq -er '.token | select(type == "string" and length > 0)'
  )"
  encoded_connect_token="$(printf '%s' "${connect_token}" | jq -sRr @uri)"
  source_endpoint="${source_endpoint}?rstream.token=${encoded_connect_token}"
fi
viewer_endpoint="${source_endpoint}"
if [[ "${uses_adapter}" == true ]]; then
  docker run --detach \
    --name "${distributor_name}" \
    --network "${network_name}" \
    --network-alias distributor \
    --read-only \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m \
    --env "RSTREAM_SOURCE_URL=${source_endpoint}" \
    --env RSTREAM_MEDIAMTX_URL=http://127.0.0.1:8889 \
    --mount "type=bind,source=${script_directory}/mediamtx.yml,target=/qualification/mediamtx.yml,readonly" \
    "${distributor_image}" /qualification/mediamtx.yml >/dev/null
  distributor_started=1
  viewer_endpoint=http://distributor:8889/camera/whep
fi
if [[ "${distribution_mode}" == mediamtx-native ]]; then
  native_source_endpoint="${source_base%/}/whep"
  native_source_endpoint="${native_source_endpoint/#https:\/\//wheps://}"
  native_source_endpoint="${native_source_endpoint/#http:\/\//whep://}"
  native_bearer_token=""
  if [[ "${edge_auth}" == true ]]; then
    native_bearer_token="${connect_token}"
  fi
  native_config="${runtime_directory}/native-mediamtx.json"
  jq -n --arg source "${native_source_endpoint}" --arg bearer "${native_bearer_token}" '{
    logLevel: "info",
    logDestinations: ["stdout"],
    api: false,
    metrics: true,
    metricsAddress: ":9998",
    pprof: false,
    playback: false,
    rtsp: false,
    rtmp: false,
    hls: false,
    srt: false,
    moq: false,
    webrtc: true,
    webrtcAddress: ":8889",
    webrtcEncryption: false,
    webrtcAllowOrigins: ["*"],
    webrtcLocalUDPAddress: ":8189",
    webrtcLocalTCPAddress: "",
    webrtcIPsFromInterfaces: true,
    webrtcIPsFromInterfacesList: [],
    webrtcAdditionalHosts: [],
    webrtcICEServers2: [],
    webrtcTrackGatherTimeout: "250ms",
    pathDefaults: {
      source: "publisher",
      maxReaders: 8
    },
    paths: {
      camera: {
        source: $source,
        whepBearerToken: $bearer,
        whepTrackGatherTimeout: "250ms",
        sourceOnDemand: true,
        sourceOnDemandStartTimeout: "15s",
        sourceOnDemandCloseAfter: "1s"
      }
    }
  }' >"${native_config}"
  chmod 0600 "${native_config}"
  docker run --detach \
    --name "${distributor_name}" \
    --network "${network_name}" \
    --network-alias distributor \
    --user "${container_user}" \
    --read-only \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m \
    --entrypoint /usr/local/bin/mediamtx \
    --mount "type=bind,source=${native_config},target=/qualification/native-mediamtx.json,readonly" \
    "${distributor_image}" /qualification/native-mediamtx.json >/dev/null
  distributor_started=1
  viewer_endpoint=http://distributor:8889/camera/whep
fi

docker run --detach \
  --name "${browser_name}" \
  --network "${network_name}" \
  --user "${container_user}" \
  --read-only \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,nosuid,size=512m \
  --shm-size 256m \
  --env HOME=/tmp \
  --mount "type=bind,source=${output_directory},target=/artifacts" \
  --mount "type=bind,source=${control_directory},target=/runtime,readonly" \
  "${browser_image}" \
  --whep-endpoint "${viewer_endpoint}" \
  --producer-metrics-url http://producer:9090/metrics \
  --output-directory /artifacts \
  --phase-file /runtime/phase.json \
  --ice-policy direct \
  --browser-executable /usr/bin/chromium \
  --browser-sandbox disabled \
  --playout-delay-hint-seconds "${playout_delay_hint_seconds}" \
  --maximum-duration-seconds "${collector_maximum_duration_seconds}" >/dev/null
browser_started=1

for _ in $(seq 1 90); do
  if [[ -s "${output_directory}/collector-ready.json" ]]; then
    break
  fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "${browser_name}")" != true ]]; then
    printf 'browser collector exited before media became ready\n' >&2
    exit 1
  fi
  sleep 1
done
if [[ ! -s "${output_directory}/collector-ready.json" ]]; then
  printf 'distributed media did not become ready within 90 seconds\n' >&2
  exit 1
fi
if [[ "${uses_adapter}" == true ]]; then
  for _ in $(seq 1 20); do
    if docker exec "${distributor_name}" wget -q -T 2 -O - http://127.0.0.1:9999/metrics \
      >"${output_directory}/adapter-metrics-active.prom" \
      && grep -Eq '^rstream_video_distributor_children\{state="active"\} 1$' "${output_directory}/adapter-metrics-active.prom" \
      && grep -Eq '^rstream_video_distributor_source_packets_total\{kind="media"\} [1-9][0-9]*$' "${output_directory}/adapter-metrics-active.prom"; then
      break
    fi
    sleep 1
  done
  if ! grep -Eq '^rstream_video_distributor_children\{state="active"\} 1$' "${output_directory}/adapter-metrics-active.prom" \
    || ! grep -Eq '^rstream_video_distributor_source_packets_total\{kind="media"\} [1-9][0-9]*$' "${output_directory}/adapter-metrics-active.prom"; then
    printf 'adapter OpenMetrics did not expose one active child with media\n' >&2
    exit 1
  fi
fi
if [[ "${distribution_mode}" == mediamtx-native ]]; then
  for _ in $(seq 1 20); do
    if docker exec "${distributor_name}" wget -q -T 2 -O - http://producer:9090/metrics \
      >"${output_directory}/producer-metrics-native-active.prom" \
      && grep -Eq '^rstream_video_producer_sessions\{state="active"\} 1$' "${output_directory}/producer-metrics-native-active.prom"; then
      break
    fi
    sleep 1
  done
  if ! grep -Eq '^rstream_video_producer_sessions\{state="active"\} 1$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_whep_initial_requests_total\{outcome="created"\} 1$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_transport_negotiated_sessions\{feature="twcc"\} 1$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_transport_negotiated_sessions\{feature="nack"\} 1$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_transport_negotiated_sessions\{feature="rtx"\} 0$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_transport_negotiated_sessions\{feature="flexfec"\} 0$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_adaptive_bitrate_updates_total\{outcome="applied"\} 0$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_adaptive_bitrate_updates_total\{outcome="failed"\} 0$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_encoder_target_bytes_per_second [1-9][0-9]*([.][0-9]+)?$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_pacer_queue_dropped_packets_total 0$' "${output_directory}/producer-metrics-native-active.prom" \
    || ! grep -Eq '^rstream_video_producer_pacer_media_dropped_frames_total 0$' "${output_directory}/producer-metrics-native-active.prom"; then
    printf 'native MediaMTX source did not negotiate its bounded producer profile\n' >&2
    exit 1
  fi
  jq -n '{
    required: true,
    activeSessions: 1,
    createdSessions: 1,
    negotiated: {twcc: 1, nack: 1, rtx: 0, flexfec: 0},
    fixedSourcePacing: {adaptiveUpdates: 0, adaptiveFailures: 0, queueDrops: 0, mediaFrameDrops: 0},
    activeAfterTeardown: null
  }' >"${output_directory}/native-source-profile.json"
fi
resource_container_names=("${producer_name}" "${browser_name}")
if [[ "${uses_mediamtx}" == true ]]; then
  resource_container_names+=("${distributor_name}")
fi
(
  while sample_container_resources; do
    sleep 1
  done
) >"${output_directory}/resource-samples.jsonl" &
resource_sampler_pid=$!

sleep "${warmup_seconds}"
write_phase baseline
if [[ "${viewer_network_enabled}" == true || "${source_network_enabled}" == true ]]; then
  sleep "${duration_seconds}"
  if [[ "${source_network_enabled}" == true ]]; then
    impairment_name="source-network"
    impairment_scope="producer-to-adapter"
    impairment_target_name="${producer_name}"
    impairment_destination_ip="$(docker inspect --format "{{with index .NetworkSettings.Networks \"${network_name}\"}}{{.IPAddress}}{{end}}" "${distributor_name}")"
    impairment_destination_port=""
    impairment_loss_percent="${source_loss_percent}"
    impairment_capacity_kbps="${source_capacity_kbps}"
    impairment_delay_milliseconds="${source_delay_milliseconds}"
    impairment_jitter_milliseconds="${source_jitter_milliseconds}"
    impairment_queue_packets="${source_queue_packets}"
  else
    impairment_name="viewer-network"
    if [[ "${uses_mediamtx}" == true ]]; then
      impairment_scope="distributor-to-browser"
      impairment_target_name="${distributor_name}"
    else
      impairment_scope="producer-to-browser"
      impairment_target_name="${producer_name}"
    fi
    impairment_destination_ip="$(docker inspect --format "{{with index .NetworkSettings.Networks \"${network_name}\"}}{{.IPAddress}}{{end}}" "${browser_name}")"
    impairment_destination_port="$(jq -er '.mediaDestinationPort | select(type == "number" and . >= 1 and . <= 65535)' "${output_directory}/collector-ready.json")"
    impairment_loss_percent="${viewer_loss_percent}"
    impairment_capacity_kbps="${viewer_capacity_kbps}"
    impairment_delay_milliseconds="${viewer_delay_milliseconds}"
    impairment_jitter_milliseconds="${viewer_jitter_milliseconds}"
    impairment_queue_packets="${viewer_queue_packets}"
  fi
  if ! [[ "${impairment_destination_ip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    printf 'network impairment destination is not a usable IPv4 address: %s\n' "${impairment_destination_ip}" >&2
    exit 1
  fi
  # shellcheck disable=SC2016
  network_helper '
    tc qdisc add dev eth0 root handle 1: prio bands 3 priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
    set -- tc qdisc add dev eth0 parent 1:3 handle 30: netem limit "${QUEUE_PACKETS}"
    if [ "${CAPACITY_KBPS}" -gt 0 ]; then
      set -- "$@" rate "${CAPACITY_KBPS}kbit"
    fi
    if [ "${DELAY_MILLISECONDS}" -gt 0 ]; then
      set -- "$@" delay "${DELAY_MILLISECONDS}ms"
      if [ "${JITTER_MILLISECONDS}" -gt 0 ]; then
        set -- "$@" "${JITTER_MILLISECONDS}ms" distribution normal
      fi
    fi
    set -- "$@" loss random "${LOSS_PERCENT}%"
    "$@"
    set -- tc filter add dev eth0 protocol ip parent 1:0 prio 1 u32 \
      match ip protocol 17 0xff \
      match ip dst "${DESTINATION_IP}/32"
    if [ -n "${DESTINATION_PORT}" ]; then
      set -- "$@" match ip dport "${DESTINATION_PORT}" 0xffff
    fi
    "$@" flowid 1:3
  '
  write_phase "${impairment_name}"
  sleep "${duration_seconds}"
  network_helper 'tc -j -s qdisc show dev eth0' >"${output_directory}/${impairment_name}-qdiscs.json"
  network_helper 'tc -j -s filter show dev eth0 parent 1:0' >"${output_directory}/${impairment_name}-filters.json"
  jq -n \
    --arg scope "${impairment_scope}" \
    --arg destination_ip "${impairment_destination_ip}" \
    --arg destination_port "${impairment_destination_port}" \
    --argjson loss_percent "${impairment_loss_percent}" \
    --argjson capacity_kbps "${impairment_capacity_kbps}" \
    --argjson delay_milliseconds "${impairment_delay_milliseconds}" \
    --argjson jitter_milliseconds "${impairment_jitter_milliseconds}" \
    --argjson queue_packets "${impairment_queue_packets}" \
    --slurpfile qdiscs "${output_directory}/${impairment_name}-qdiscs.json" \
    --slurpfile filters "${output_directory}/${impairment_name}-filters.json" '
      {
        enabled: true,
        scope: $scope,
        destination: {
          ip: $destination_ip,
          port: (if $destination_port == "" then null else ($destination_port | tonumber) end)
        },
        capacityKbps: $capacity_kbps,
        delayMilliseconds: $delay_milliseconds,
        jitterMilliseconds: $jitter_milliseconds,
        lossPercent: $loss_percent,
        queuePackets: $queue_packets,
        qdisc: ([$qdiscs[0][] | select(.kind == "netem" and .handle == "30:")] | first),
        filters: $filters[0]
      }
    ' >"${output_directory}/${impairment_name}.json"
  network_helper 'tc qdisc del dev eth0 root'
  write_phase recovery
  sleep "${recovery_seconds}"
else
  sleep "${duration_seconds}"
fi
write_phase complete
browser_status="$(docker wait "${browser_name}")"
stop_resource_sampler
capture_logs
if [[ "${browser_status}" != 0 ]]; then
  printf 'browser collector exited with status %s\n' "${browser_status}" >&2
  exit 1
fi
sleep 3
capture_logs
if [[ "${uses_adapter}" == true ]]; then
  docker exec "${distributor_name}" wget -q -T 2 -O - http://127.0.0.1:9999/metrics \
    >"${output_directory}/adapter-metrics-final.prom"
  for state in active backoff idle; do
    if ! grep -Eq "^rstream_video_distributor_children\\{state=\"${state}\"\\} 0$" "${output_directory}/adapter-metrics-final.prom"; then
      printf 'adapter OpenMetrics retained a %s child after teardown\n' "${state}" >&2
      exit 1
    fi
  done
  if ! grep -Eq '^rstream_video_distributor_attempts_total\{outcome="canceled"\} [1-9][0-9]*$' "${output_directory}/adapter-metrics-final.prom" \
    || ! grep -Eq '^rstream_video_distributor_telemetry_dropped_snapshots_total 0$' "${output_directory}/adapter-metrics-final.prom" \
    || ! grep -Eq '^rstream_video_distributor_telemetry_invalid_messages_total 0$' "${output_directory}/adapter-metrics-final.prom" \
    || ! grep -Eq '^rstream_video_distributor_telemetry_stale_processes_total 0$' "${output_directory}/adapter-metrics-final.prom"; then
    printf 'adapter OpenMetrics lifecycle or IPC integrity gate failed\n' >&2
    exit 1
  fi
  jq -Rn '[inputs | fromjson? | select(.msg == "video distributor attempt stopped")] | last' \
    <"${output_directory}/distributor.log" >"${output_directory}/adapter-result.json"
  if [[ "$(jq -r 'type' "${output_directory}/adapter-result.json")" != object ]]; then
    printf 'distributor did not emit its terminal adapter statistics\n' >&2
    exit 1
  fi
fi
if [[ "${distribution_mode}" == mediamtx-native ]]; then
  for _ in $(seq 1 20); do
    if docker exec "${distributor_name}" wget -q -T 2 -O - http://producer:9090/metrics \
      >"${output_directory}/producer-metrics-native-final.prom" \
      && grep -Eq '^rstream_video_producer_sessions\{state="active"\} 0$' "${output_directory}/producer-metrics-native-final.prom"; then
      break
    fi
    sleep 1
  done
  if ! grep -Eq '^rstream_video_producer_sessions\{state="active"\} 0$' "${output_directory}/producer-metrics-native-final.prom" \
    || ! grep -Eq '^rstream_video_producer_whep_initial_requests_total\{outcome="created"\} 1$' "${output_directory}/producer-metrics-native-final.prom" \
    || ! grep -Eq '^rstream_video_producer_pacer_queue_dropped_packets_total 0$' "${output_directory}/producer-metrics-native-final.prom" \
    || ! grep -Eq '^rstream_video_producer_pacer_media_dropped_frames_total 0$' "${output_directory}/producer-metrics-native-final.prom"; then
    printf 'native MediaMTX retained or recreated its producer source after viewer teardown\n' >&2
    exit 1
  fi
  jq '.activeAfterTeardown = 0' "${output_directory}/native-source-profile.json" \
    >"${output_directory}/native-source-profile.tmp.json"
  mv "${output_directory}/native-source-profile.tmp.json" "${output_directory}/native-source-profile.json"
fi
runtime_log_files=("${output_directory}/producer.log")
if [[ "${uses_mediamtx}" == true ]]; then
  runtime_log_files+=("${output_directory}/distributor.log")
fi
count_runtime_matches() {
  local pattern="$1"
  {
    grep -h -E -i -c -- "${pattern}" "${runtime_log_files[@]}" || true
  } | awk '{sum += $1} END {print sum + 0}'
}
runtime_fatal_errors="$(count_runtime_matches '(^|[[:space:]])(ERR|FTL)([[:space:]]|$)|panic:|fatal error:')"
runtime_h264_errors="$(count_runtime_matches 'invalid FU-A packet|non-starting FU-A|invalid H264|invalid NALU|unable to decode.*H264')"
runtime_packet_loss_warnings="$(count_runtime_matches '[0-9]+ RTP packets lost')"
runtime_transport_buffer_warnings="$(count_runtime_matches 'failed to sufficiently increase (receive|send) buffer size')"
jq -n \
  --argjson fatal_errors "${runtime_fatal_errors}" \
  --argjson h264_errors "${runtime_h264_errors}" \
  --argjson packet_loss_warnings "${runtime_packet_loss_warnings}" \
  --argjson transport_buffer_warnings "${runtime_transport_buffer_warnings}" \
  '{
    fatalErrors: $fatal_errors,
    h264PacketizationErrors: $h264_errors,
    packetLossWarnings: $packet_loss_warnings,
    transportBufferWarnings: $transport_buffer_warnings
  }' >"${output_directory}/runtime-health.json"

if [[ ! -s "${output_directory}/resource-samples.jsonl" ]]; then
  printf 'container resource sampler did not produce data\n' >&2
  exit 1
fi
jq -s \
  --arg producer_name "${producer_name}" \
  --arg browser_name "${browser_name}" \
  --arg distributor_name "$(if [[ "${uses_mediamtx}" == true ]]; then printf '%s' "${distributor_name}"; fi)" \
  -f "${script_directory}/resource-report.jq" \
  "${output_directory}/resource-samples.jsonl" >"${output_directory}/resources.json"
required_resource_components='["browser", "producer"]'
if [[ "${uses_mediamtx}" == true ]]; then
  required_resource_components='["browser", "distributor", "producer"]'
fi
if ! jq -e --argjson required "${required_resource_components}" '
  (.components | keys | sort) == $required and
  ([.components[] | .samples >= 2 and .cpuCoreRatio.maximum >= 0 and .residentBytes.maximum > 0 and .tasks.maximum > 0] | all)
' "${output_directory}/resources.json" >/dev/null; then
  printf 'container resource report is incomplete\n' >&2
  exit 1
fi

jq -s \
  --arg revision "${revision}" \
  --arg mode "${distribution_mode}" \
  --argjson edge_auth "${edge_auth}" \
  --argjson connect_token_ttl_seconds "${connect_token_ttl_seconds}" \
  --argjson working_tree_dirty "${working_tree_dirty}" \
  --arg producer_image "$(docker image inspect --format '{{.Id}}' "${producer_image}")" \
  --arg distributor_image "$(if [[ "${uses_mediamtx}" == true ]]; then docker image inspect --format '{{.Id}}' "${distributor_image}"; fi)" \
  --arg browser_image "$(docker image inspect --format '{{.Id}}' "${browser_image}")" \
  --slurpfile adapter "${output_directory}/adapter-result.json" \
  --slurpfile runtime_health "${output_directory}/runtime-health.json" \
  --slurpfile browser "${output_directory}/browser.json" \
  --slurpfile signaling "${output_directory}/signaling-events.json" \
  --slurpfile resources "${output_directory}/resources.json" \
  --argjson warmup_seconds "${warmup_seconds}" \
  --argjson phase_seconds "${duration_seconds}" \
  --argjson recovery_seconds "${recovery_seconds}" \
  --argjson flexfec_media_packets "${flexfec_media_packets}" \
  --argjson flexfec_repair_packets "${flexfec_repair_packets}" \
  --argjson playout_delay_hint_seconds "${playout_delay_hint_seconds}" \
  --slurpfile viewer_network "${output_directory}/viewer-network.json" \
  --slurpfile source_network "${output_directory}/source-network.json" \
  --slurpfile native_source_profile "${output_directory}/native-source-profile.json" \
  -f "${script_directory}/result.jq" \
  "${output_directory}/samples.jsonl" >"${output_directory}/result.json"

node "${script_directory}/sanitize-artifacts.mjs" "${output_directory}"

if [[ "$(jq -r '.passed' "${output_directory}/result.json")" != true ]]; then
  jq '.gates' "${output_directory}/result.json" >&2
  if [[ "$(jq -r '.gates.performanceEnvironment' "${output_directory}/result.json")" != true ]]; then
    printf 'qualification host transport buffers are insufficient; inspect runtime-health.json and producer.log\n' >&2
  fi
  printf 'distributed end-to-end qualification failed\n' >&2
  exit 1
fi
printf 'Distributed end-to-end qualification passed: %s\n' "${output_directory}/result.json"
