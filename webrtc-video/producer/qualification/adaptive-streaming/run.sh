#!/usr/bin/env bash

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
producer_directory="$(cd "${script_directory}/../.." && pwd -P)"
repository_directory="$(git -C "${producer_directory}" rev-parse --show-toplevel)"
context_name="${RSTREAM_CONTEXT:-}"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
output_directory="${1:-${script_directory}/.artifacts/${timestamp}}"
container_name="rstream-adaptive-${timestamp}-$$"
container_name="${container_name//[^a-z0-9_.-]/-}"
browser_container_name="${container_name}-browser"
network_name="${container_name}-network"
producer_primary_network="${container_name}-producer-primary"
producer_secondary_network="${container_name}-producer-secondary"
producer_runtime_volume="${container_name}-runtime"
producer_seed_container="${container_name}-seed"
revision="$(git -C "${repository_directory}" rev-parse HEAD)"
producer_tree="$(git -C "${repository_directory}" rev-parse HEAD:webrtc-video/producer)"
image_tag="rstream-webrtc-adaptive-qualification:${revision:0:12}"
browser_image_tag="rstream-webrtc-adaptive-browser:${revision:0:12}"
browser_user="$(id -u):$(id -g)"
runtime_directory=""
collector_pid=""
log_pid=""
receiver_sampler_pid=""
producer_sampler_pid=""
host_cpu_sampler_pid=""
receiver_host_cpu_sampler_pid=""
traffic_control_pid=""
container_started=0
browser_container_started=0
network_created=0
producer_primary_network_created=0
producer_secondary_network_created=0
producer_runtime_volume_created=0
producer_seed_container_created=0
encoder_evidence_captured=0
host_cpu_evidence_captured=0
media_destination_address=""
media_destination_family=""
media_destination_port=""
media_destination_protocol=""
match_destination_port=""
impairment_scope=""
path_kind="${RSTREAM_QUALIFICATION_PATH:-relay}"
protection_profile="${RSTREAM_QUALIFICATION_PROTECTION:-nack-rtx}"
flexfec_enabled=false
flexfec_media_packets="${RSTREAM_QUALIFICATION_FLEXFEC_MEDIA_PACKETS:-4}"
flexfec_repair_packets="${RSTREAM_QUALIFICATION_FLEXFEC_REPAIR_PACKETS:-2}"
warmup_seconds="${RSTREAM_QUALIFICATION_WARMUP_SECONDS:-20}"
baseline_seconds="${RSTREAM_QUALIFICATION_BASELINE_SECONDS:-25}"
constrained_seconds="${RSTREAM_QUALIFICATION_CONSTRAINED_SECONDS:-45}"
impaired_seconds="${RSTREAM_QUALIFICATION_IMPAIRED_SECONDS:-35}"
recovery_seconds="${RSTREAM_QUALIFICATION_RECOVERY_SECONDS:-45}"
mobility_seconds="${RSTREAM_QUALIFICATION_MOBILITY_SECONDS:-30}"
mobility_mode="${RSTREAM_QUALIFICATION_MOBILITY:-off}"
transition_step_seconds="${RSTREAM_QUALIFICATION_TRANSITION_STEP_SECONDS:-5}"
capacity_kbps="${RSTREAM_QUALIFICATION_CAPACITY_KBPS:-4000}"
queue_limit_packets="${RSTREAM_QUALIFICATION_QUEUE_LIMIT_PACKETS:-256}"
docker_pull="${RSTREAM_QUALIFICATION_DOCKER_PULL:-always}"
producer_docker_context="${RSTREAM_QUALIFICATION_PRODUCER_DOCKER_CONTEXT:-}"
producer_docker_host="${RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST:-}"
prepare_context_binary="${RSTREAM_QUALIFICATION_PREPARE_CONTEXT_BINARY:-}"
prepared_runtime_directory="${RSTREAM_QUALIFICATION_PREPARED_RUNTIME_DIRECTORY:-}"
pion_log_debug="${RSTREAM_QUALIFICATION_PION_LOG_DEBUG:-}"
encoder_debug="${RSTREAM_QUALIFICATION_ENCODER_DEBUG:-true}"
playout_delay_hint_seconds="${RSTREAM_QUALIFICATION_PLAYOUT_DELAY_HINT_SECONDS:-0}"
turn_transport="${RSTREAM_QUALIFICATION_TURN_TRANSPORT:-}"
producer_turn_policy="${RSTREAM_QUALIFICATION_PRODUCER_TURN_POLICY:-}"
if [[ -z "${producer_turn_policy}" ]]; then
  if [[ "${path_kind}" == "relay" ]]; then
    producer_turn_policy="relay"
  else
    producer_turn_policy="disabled"
  fi
fi
if [[ -z "${turn_transport}" ]] && [[ "${path_kind}" == "relay" ]] && \
  [[ "${producer_turn_policy}" == "relay" ]]; then
  turn_transport="udp"
fi
case "${encoder_debug}" in
true | false)
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_ENCODER_DEBUG must be true or false\n' >&2
  exit 1
  ;;
esac
if ! jq -en --arg value "${playout_delay_hint_seconds}" \
  '($value | tonumber) as $number | $number >= 0 and $number <= 1' >/dev/null; then
  printf 'RSTREAM_QUALIFICATION_PLAYOUT_DELAY_HINT_SECONDS must be between 0 and 1\n' >&2
  exit 1
fi

producer_docker() {
  local -a selector=()
  if [[ -n "${producer_docker_context}" ]]; then
    selector+=(--context "${producer_docker_context}")
  fi
  if [[ -n "${producer_docker_host}" ]]; then
    selector+=(--host "${producer_docker_host}")
  fi
  docker "${selector[@]}" "$@"
}

producer_is_remote() {
  [[ -n "${producer_docker_context}" || -n "${producer_docker_host}" ]]
}

capture_encoder_evidence() {
  if ((container_started != 1 || encoder_evidence_captured == 1)); then
    return 0
  fi
  local temporary="${output_directory}/encoder.log.tmp"
  if producer_docker exec "${container_name}" sh -c \
    'test -s /tmp/rstream-qualification-encoder.log && cat /tmp/rstream-qualification-encoder.log' \
    >"${temporary}"; then
    mv "${temporary}" "${output_directory}/encoder.log"
    encoder_evidence_captured=1
    return 0
  fi
  rm -f "${temporary}"
  return 1
}

capture_host_cpu_evidence() {
  if ((container_started != 1 || host_cpu_evidence_captured == 1)); then
    return 0
  fi
  local temporary="${output_directory}/producer-host-cpu.jsonl.tmp"
  if producer_docker exec "${container_name}" sh -c \
    'test -s /tmp/rstream-qualification-host-cpu.jsonl && cat /tmp/rstream-qualification-host-cpu.jsonl' \
    >"${temporary}"; then
    mv "${temporary}" "${output_directory}/producer-host-cpu.jsonl"
    host_cpu_evidence_captured=1
    return 0
  fi
  rm -f "${temporary}"
  return 1
}

stop_host_cpu_sampler() {
  local status=0
  local wait_status=0
  if [[ -z "${host_cpu_sampler_pid}" ]]; then
    return 0
  fi
  # The command substitution belongs to the shell inside the container.
  # shellcheck disable=SC2016
  producer_docker exec "${container_name}" sh -c \
    'test -s /tmp/rstream-qualification-host-cpu.pid && kill -TERM "$(cat /tmp/rstream-qualification-host-cpu.pid)"' \
    >/dev/null 2>&1 || status=$?
  if ((status != 0)); then
    kill -TERM "${host_cpu_sampler_pid}" 2>/dev/null || true
  fi
  for _ in $(seq 1 50); do
    if ! kill -0 "${host_cpu_sampler_pid}" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  if kill -0 "${host_cpu_sampler_pid}" 2>/dev/null; then
    kill -KILL "${host_cpu_sampler_pid}" 2>/dev/null || true
    status=1
  fi
  wait "${host_cpu_sampler_pid}" || wait_status=$?
  if ((status == 0 && wait_status != 0)); then
    status="${wait_status}"
  fi
  host_cpu_sampler_pid=""
  return "${status}"
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  if [[ -n "${collector_pid}" ]] && kill -0 "${collector_pid}" 2>/dev/null; then
    kill -TERM "${collector_pid}" 2>/dev/null
    wait "${collector_pid}" 2>/dev/null
  fi
  if [[ -n "${log_pid}" ]] && kill -0 "${log_pid}" 2>/dev/null; then
    kill -TERM "${log_pid}" 2>/dev/null
    wait "${log_pid}" 2>/dev/null
  fi
  if [[ -n "${receiver_sampler_pid}" ]] && \
    kill -0 "${receiver_sampler_pid}" 2>/dev/null; then
    if ((browser_container_started == 1)); then
      docker exec "${browser_container_name}" sh -c \
        'test ! -s /tmp/receiver-udp-sampler.pid || kill -TERM "$(cat /tmp/receiver-udp-sampler.pid)"' \
        >/dev/null 2>&1
    fi
    kill -TERM "${receiver_sampler_pid}" 2>/dev/null
    wait "${receiver_sampler_pid}" 2>/dev/null
  fi
  if [[ -n "${producer_sampler_pid}" ]] && \
    kill -0 "${producer_sampler_pid}" 2>/dev/null; then
    kill -TERM "${producer_sampler_pid}" 2>/dev/null
    wait "${producer_sampler_pid}" 2>/dev/null
  fi
  if [[ -n "${host_cpu_sampler_pid}" ]] && \
    kill -0 "${host_cpu_sampler_pid}" 2>/dev/null; then
    stop_host_cpu_sampler || true
  fi
  if [[ -n "${receiver_host_cpu_sampler_pid}" ]] && \
    kill -0 "${receiver_host_cpu_sampler_pid}" 2>/dev/null; then
    stop_receiver_host_cpu_sampler || true
  fi
  if [[ -n "${traffic_control_pid}" ]] && \
    kill -0 "${traffic_control_pid}" 2>/dev/null; then
    kill -TERM "${traffic_control_pid}" 2>/dev/null
    wait "${traffic_control_pid}" 2>/dev/null
  fi
  if ((browser_container_started == 1)); then
    docker rm --force "${browser_container_name}" >/dev/null 2>&1
  fi
  if ((container_started == 1)); then
    capture_host_cpu_evidence || true
    capture_encoder_evidence || true
    producer_docker rm --force "${container_name}" >/dev/null 2>&1
  fi
  if ((producer_seed_container_created == 1)); then
    producer_docker rm --force "${producer_seed_container}" >/dev/null 2>&1
  fi
  if ((producer_runtime_volume_created == 1)); then
    producer_docker volume rm "${producer_runtime_volume}" >/dev/null 2>&1
  fi
  if ((network_created == 1)); then
    docker network rm "${network_name}" >/dev/null 2>&1
  fi
  if ((producer_primary_network_created == 1)); then
    producer_docker network rm "${producer_primary_network}" >/dev/null 2>&1
  fi
  if ((producer_secondary_network_created == 1)); then
    producer_docker network rm "${producer_secondary_network}" >/dev/null 2>&1
  fi
  if [[ -d "${runtime_directory}" ]]; then
    find "${runtime_directory}" -depth -delete
  fi
  exit "${status}"
}
trap cleanup EXIT INT TERM
runtime_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-adaptive-runtime.XXXXXX")"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

positive_integer() {
  [[ "$1" =~ ^[0-9]{1,9}$ ]] && ((10#$1 > 0))
}

write_phase() {
  local name="$1"
  local shaping="$2"
  local temporary="${runtime_directory}/phase.json.tmp"
  local started_at
  started_at="$(node -e 'process.stdout.write(new Date().toISOString())')"
  jq -n \
    --arg name "${name}" \
    --arg started_at "${started_at}" \
    --argjson shaping "${shaping}" \
    '{name: $name, startedAt: $started_at, shaping: $shaping}' >"${temporary}"
  mv "${temporary}" "${runtime_directory}/phase.json"
  jq -cn \
    --arg name "${name}" \
    --arg started_at "${started_at}" \
    '{name: $name, startedAt: $started_at}' \
    >>"${output_directory}/phase-timeline.jsonl"
}

record_setup_milestone() {
  local name="$1"
  local observed_at
  observed_at="$(node -e 'process.stdout.write(new Date().toISOString())')"
  jq -cn \
    --arg name "${name}" \
    --arg observed_at "${observed_at}" \
    '{name: $name, observedAt: $observed_at}' \
    >>"${output_directory}/setup-timeline.jsonl"
}

run_phase() {
  local name="$1"
  local duration="$2"
  local shaping="$3"
  write_phase "${name}" "${shaping}"
  hold_phase "${name}" "${duration}"
}

start_traffic_control() {
  local -a route_arguments=()
  local network_interface=""
  if [[ "${media_destination_family}" == "6" ]]; then
    route_arguments=(-6)
  fi
  network_interface="$(producer_docker exec "${container_name}" \
    ip "${route_arguments[@]}" route get "${media_destination_address}" | \
    sed -nE 's/.* dev ([^ ]+).*/\1/p' | head -1)"
  if ! [[ "${network_interface}" =~ ^[a-zA-Z0-9_.:@-]+$ ]]; then
    printf 'failed to resolve the active interface for %s\n' \
      "${media_destination_address}" >&2
    exit 1
  fi
  producer_docker exec --interactive --user 0 "${container_name}" \
    sh -s -- \
    --evidence-directory /tmp/rstream-network-evidence \
    --network-interface "${network_interface}" \
    --address-family "${media_destination_family}" \
    --destination-address "${media_destination_address}" \
    --destination-port "${media_destination_port}" \
    --transport-protocol "${media_destination_protocol}" \
    --match-destination-port "${match_destination_port}" \
    --queue-limit-packets "${queue_limit_packets}" \
    --capacity-step-one-kbps "${capacity_step_one_kbps}" \
    --capacity-step-two-kbps "${capacity_step_two_kbps}" \
    --capacity-step-three-kbps "${capacity_step_three_kbps}" \
    --capacity-kbps "${capacity_kbps}" \
    --transition-step-seconds "${transition_step_seconds}" \
    --constrained-steady-seconds "${constrained_steady_seconds}" \
    --impaired-seconds "${impaired_seconds}" \
    --recovery-capacity-kbps "${recovery_capacity_kbps}" \
    --recovery-seconds "${recovery_seconds}" \
    <"${script_directory}/traffic-control.sh" \
    >"${output_directory}/traffic-control-events.log" \
    2>"${output_directory}/traffic-control.log" &
  traffic_control_pid=$!
}

wait_for_traffic_control_event() {
  local event="$1"
  local timeout_seconds="$2"
  local deadline=$((SECONDS + timeout_seconds))
  while ! grep -Fxq "${event}" "${output_directory}/traffic-control-events.log"; do
    if ! kill -0 "${traffic_control_pid}" 2>/dev/null; then
      local status=0
      wait "${traffic_control_pid}" || status=$?
      traffic_control_pid=""
      printf 'traffic-control scheduler exited before %s (status %s)\n' \
        "${event}" "${status}" >&2
      cat "${output_directory}/traffic-control.log" >&2
      return 1
    fi
    if ((SECONDS >= deadline)); then
      printf 'timed out waiting for traffic-control event %s\n' "${event}" >&2
      return 1
    fi
    sleep 0.1
  done
}

wait_for_traffic_control() {
  local status=0
  wait "${traffic_control_pid}" || status=$?
  traffic_control_pid=""
  if ((status != 0)); then
    printf 'traffic-control scheduler failed with status %s\n' "${status}" >&2
    cat "${output_directory}/traffic-control.log" >&2
    return "${status}"
  fi
}

start_host_cpu_sampler() {
  producer_docker exec "${container_name}" sh -c \
    'echo $$ >/tmp/rstream-qualification-host-cpu.pid; exec /usr/local/bin/rstream-sample-host-cpu /tmp/rstream-qualification-host-cpu.jsonl' \
    >"${output_directory}/producer-host-cpu-sampler.log" 2>&1 &
  host_cpu_sampler_pid=$!
}

start_receiver_host_cpu_sampler() {
  docker exec "${browser_container_name}" sh -c \
    'echo $$ >/tmp/rstream-qualification-receiver-host-cpu.pid; exec /usr/local/bin/rstream-sample-host-cpu /artifacts/receiver-host-cpu.jsonl' \
    >"${output_directory}/receiver-host-cpu-sampler.log" 2>&1 &
  receiver_host_cpu_sampler_pid=$!
}

stop_receiver_host_cpu_sampler() {
  local status=0
  local wait_status=0
  if [[ -z "${receiver_host_cpu_sampler_pid}" ]]; then
    return 0
  fi
  # The command substitution belongs to the shell inside the container.
  # shellcheck disable=SC2016
  docker exec "${browser_container_name}" sh -c \
    'test -s /tmp/rstream-qualification-receiver-host-cpu.pid && kill -TERM "$(cat /tmp/rstream-qualification-receiver-host-cpu.pid)"' \
    >/dev/null 2>&1 || status=$?
  if ((status != 0)); then
    kill -TERM "${receiver_host_cpu_sampler_pid}" 2>/dev/null || true
  fi
  for _ in $(seq 1 50); do
    if ! kill -0 "${receiver_host_cpu_sampler_pid}" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  if kill -0 "${receiver_host_cpu_sampler_pid}" 2>/dev/null; then
    kill -KILL "${receiver_host_cpu_sampler_pid}" 2>/dev/null || true
    status=1
  fi
  wait "${receiver_host_cpu_sampler_pid}" || wait_status=$?
  if ((status == 0 && wait_status != 0)); then
    status="${wait_status}"
  fi
  receiver_host_cpu_sampler_pid=""
  return "${status}"
}

capture_network_evidence() {
  producer_docker exec "${container_name}" \
    tar -C /tmp/rstream-network-evidence -cf - . | \
    tar -C "${output_directory}" -xf -
  local expected
  for expected in \
    qdisc-constrained-step-1-start.json \
    qdisc-constrained-step-1.json \
    qdisc-constrained-step-2-start.json \
    qdisc-constrained-step-2.json \
    qdisc-constrained-step-3-start.json \
    qdisc-constrained-step-3.json \
    qdisc-constrained-steady-start.json \
    qdisc-constrained-steady.json \
    qdisc-impaired-start.json \
    qdisc-impaired.json \
    filter-constrained-step-1-start.json \
    filter-constrained-step-1.json \
    filter-constrained-step-2-start.json \
    filter-constrained-step-2.json \
    filter-constrained-step-3-start.json \
    filter-constrained-step-3.json \
    filter-constrained-steady-start.json \
    filter-constrained-steady.json \
    filter-impaired-start.json \
    filter-impaired.json \
    qdisc-recovery-start.json \
    qdisc-recovery.json \
    filter-recovery-start.json \
    filter-recovery.json; do
    if [[ ! -s "${output_directory}/${expected}" ]]; then
      printf 'traffic-control evidence is missing or empty: %s\n' \
        "${expected}" >&2
      return 1
    fi
  done
}

hold_phase() {
  local name="$1"
  local duration="$2"
  local elapsed=0
  while ((elapsed < duration)); do
    if ! kill -0 "${collector_pid}" 2>/dev/null; then
      wait "${collector_pid}"
      printf 'collector exited before phase %s completed\n' "${name}" >&2
      exit 1
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
}

wait_for_file() {
  local path="$1"
  local timeout_seconds="$2"
  local elapsed=0
  while [[ ! -s "${path}" ]]; do
    if [[ -n "${collector_pid}" ]] && ! kill -0 "${collector_pid}" 2>/dev/null; then
      wait "${collector_pid}"
      return 1
    fi
    if ((elapsed >= timeout_seconds)); then
      printf 'timed out waiting for %s\n' "${path}" >&2
      return 1
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
}

start_udp_samplers() {
  docker exec "${browser_container_name}" \
    sh -c \
    'echo $$ >/tmp/receiver-udp-sampler.pid; exec node /qualification/sample-receiver-udp.mjs --output /artifacts/receiver-udp.jsonl --phase-file /runtime/phase.json' \
    >"${output_directory}/receiver-udp-sampler.log" 2>&1 &
  receiver_sampler_pid=$!
  local -a producer_sampler_arguments=(
    --docker-container "${container_name}"
  )
  if [[ -n "${producer_docker_context}" ]]; then
    producer_sampler_arguments+=(--docker-context "${producer_docker_context}")
  fi
  if [[ -n "${producer_docker_host}" ]]; then
    producer_sampler_arguments+=(--docker-host "${producer_docker_host}")
  fi
  node "${script_directory}/sample-receiver-udp.mjs" \
    "${producer_sampler_arguments[@]}" \
    --output "${output_directory}/producer-udp.jsonl" \
    --phase-file "${runtime_directory}/phase.json" \
    >"${output_directory}/producer-udp-sampler.log" 2>&1 &
  producer_sampler_pid=$!
}

stop_udp_samplers() {
  local status=0
  local wait_status=0
  if [[ -n "${receiver_sampler_pid}" ]]; then
    docker exec "${browser_container_name}" sh -c \
      'kill -TERM "$(cat /tmp/receiver-udp-sampler.pid)"' || true
    wait "${receiver_sampler_pid}" || wait_status=$?
    if ((status == 0 && wait_status != 0)); then
      status="${wait_status}"
    fi
    receiver_sampler_pid=""
  fi
  if [[ -n "${producer_sampler_pid}" ]]; then
    wait_status=0
    kill -TERM "${producer_sampler_pid}" 2>/dev/null || true
    wait "${producer_sampler_pid}" || wait_status=$?
    if ((status == 0 && wait_status != 0)); then
      status="${wait_status}"
    fi
    producer_sampler_pid=""
  fi
  return "${status}"
}

if [[ -z "${context_name}" && -z "${prepared_runtime_directory}" ]]; then
  printf 'RSTREAM_CONTEXT must name the non-production CLI context used for qualification\n' >&2
  exit 1
fi
for command in docker git jq node tar; do
  require_command "${command}"
done
if [[ -n "${prepared_runtime_directory}" ]]; then
  require_command install
  if [[ -n "${prepare_context_binary}" ]]; then
    printf 'prepared runtime and context-preparation binary are mutually exclusive\n' >&2
    exit 1
  fi
  for runtime_file in config.yaml runtime.env relay-config.yaml direct-config.yaml; do
    runtime_path="${prepared_runtime_directory}/${runtime_file}"
    if [[ ! -f "${runtime_path}" || -L "${runtime_path}" ]]; then
      printf 'prepared runtime file is missing or not a regular file: %s\n' \
        "${runtime_path}" >&2
      exit 1
    fi
  done
elif [[ -z "${prepare_context_binary}" ]]; then
  require_command rstream
  require_command go
elif [[ ! -x "${prepare_context_binary}" ]]; then
  require_command rstream
  printf 'RSTREAM_QUALIFICATION_PREPARE_CONTEXT_BINARY is not executable: %s\n' \
    "${prepare_context_binary}" >&2
  exit 1
else
  require_command rstream
fi
if [[ -n "${producer_docker_context}" && -n "${producer_docker_host}" ]]; then
  printf 'producer Docker context and host are mutually exclusive\n' >&2
  exit 1
fi
case "${path_kind}" in
direct | relay)
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_PATH must be direct or relay\n' >&2
  exit 1
  ;;
esac
if [[ "${path_kind}" == "direct" ]] && producer_is_remote; then
  printf 'direct qualification requires producer and browser on the same Docker daemon\n' >&2
  exit 1
fi
if ! producer_docker version >/dev/null; then
  printf 'producer Docker daemon is unavailable\n' >&2
  exit 1
fi
case "${protection_profile}" in
nack-rtx)
  ;;
nack-rtx-flexfec)
  flexfec_enabled=true
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_PROTECTION must be nack-rtx or nack-rtx-flexfec\n' >&2
  exit 1
  ;;
esac
if ! positive_integer "${flexfec_media_packets}"; then
  printf 'RSTREAM_QUALIFICATION_FLEXFEC_MEDIA_PACKETS must be an integer from 1 through 110\n' >&2
  exit 1
fi
flexfec_media_packets=$((10#${flexfec_media_packets}))
if ((flexfec_media_packets > 110)); then
  printf 'RSTREAM_QUALIFICATION_FLEXFEC_MEDIA_PACKETS must be an integer from 1 through 110\n' >&2
  exit 1
fi
if ! positive_integer "${flexfec_repair_packets}"; then
  printf 'RSTREAM_QUALIFICATION_FLEXFEC_REPAIR_PACKETS must be an integer from 1 through the media-packet group size\n' >&2
  exit 1
fi
flexfec_repair_packets=$((10#${flexfec_repair_packets}))
if ((flexfec_repair_packets > 110 || flexfec_repair_packets > flexfec_media_packets)); then
  printf 'RSTREAM_QUALIFICATION_FLEXFEC_REPAIR_PACKETS must be an integer from 1 through the media-packet group size\n' >&2
  exit 1
fi
if [[ -n "${pion_log_debug}" ]] && [[ ! "${pion_log_debug}" =~ ^[A-Za-z0-9_.-]+(,[A-Za-z0-9_.-]+)*$ ]]; then
  printf 'RSTREAM_QUALIFICATION_PION_LOG_DEBUG must be a comma-separated scope list\n' >&2
  exit 1
fi
case "${turn_transport}" in
"" | udp | tcp | dtls | tls)
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_TURN_TRANSPORT must be udp, tcp, dtls, or tls\n' >&2
  exit 1
  ;;
esac
case "${producer_turn_policy}" in
disabled | auto | relay)
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_PRODUCER_TURN_POLICY must be disabled, auto, or relay\n' >&2
  exit 1
  ;;
esac
if [[ "${path_kind}" == "direct" ]] && [[ -n "${turn_transport}" ]]; then
  printf 'RSTREAM_QUALIFICATION_TURN_TRANSPORT only applies to relay runs\n' >&2
  exit 1
fi
if [[ "${path_kind}" == "direct" ]] && [[ "${producer_turn_policy}" != "disabled" ]]; then
  printf 'direct qualification requires producer TURN to be disabled\n' >&2
  exit 1
fi
for duration in \
  "${warmup_seconds}" \
  "${baseline_seconds}" \
  "${constrained_seconds}" \
  "${impaired_seconds}" \
  "${recovery_seconds}" \
  "${mobility_seconds}" \
  "${transition_step_seconds}"; do
  if ! positive_integer "${duration}"; then
    printf 'qualification phase durations must be positive integers, got %s\n' "${duration}" >&2
    exit 1
  fi
done
case "${mobility_mode}" in
off)
  ;;
producer)
  if [[ "${path_kind}" != "relay" ]]; then
    printf 'producer mobility qualification requires the relay path\n' >&2
    exit 1
  fi
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_MOBILITY must be off or producer\n' >&2
  exit 1
  ;;
esac
warmup_seconds=$((10#${warmup_seconds}))
baseline_seconds=$((10#${baseline_seconds}))
constrained_seconds=$((10#${constrained_seconds}))
impaired_seconds=$((10#${impaired_seconds}))
recovery_seconds=$((10#${recovery_seconds}))
transition_step_seconds=$((10#${transition_step_seconds}))
if ! positive_integer "${capacity_kbps}"; then
  printf 'RSTREAM_QUALIFICATION_CAPACITY_KBPS must be an integer of at least 600, got %s\n' \
    "${capacity_kbps}" >&2
  exit 1
fi
capacity_kbps=$((10#${capacity_kbps}))
if ((capacity_kbps < 600)); then
  printf 'RSTREAM_QUALIFICATION_CAPACITY_KBPS must be an integer of at least 600, got %s\n' \
    "${capacity_kbps}" >&2
  exit 1
fi
if ! positive_integer "${queue_limit_packets}"; then
  printf 'RSTREAM_QUALIFICATION_QUEUE_LIMIT_PACKETS must be an integer from 32 through 4096, got %s\n' \
    "${queue_limit_packets}" >&2
  exit 1
fi
queue_limit_packets=$((10#${queue_limit_packets}))
if ((queue_limit_packets < 32 || queue_limit_packets > 4096)); then
  printf 'RSTREAM_QUALIFICATION_QUEUE_LIMIT_PACKETS must be an integer from 32 through 4096, got %s\n' \
    "${queue_limit_packets}" >&2
  exit 1
fi
capacity_step_one_kbps=$((capacity_kbps * 4))
capacity_step_two_kbps=$((capacity_kbps * 3))
capacity_step_three_kbps=$((capacity_kbps * 2))
constrained_steady_seconds=$((constrained_seconds - 3 * transition_step_seconds))
if ((constrained_steady_seconds < 15)); then
  printf 'constrained phase must leave at least 15 steady seconds after its three transition steps\n' >&2
  exit 1
fi
if [[ -e "${output_directory}" ]] && [[ -n "$(find "${output_directory}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'output directory is not empty: %s\n' "${output_directory}" >&2
  exit 1
fi
mkdir -p "${output_directory}"
output_directory="$(cd "${output_directory}" && pwd -P)"

if [[ -n "${prepared_runtime_directory}" ]]; then
  printf 'Loading prepared credential-isolated runtime\n'
  for runtime_file in config.yaml runtime.env relay-config.yaml direct-config.yaml; do
    install -m 0600 \
      "${prepared_runtime_directory}/${runtime_file}" \
      "${runtime_directory}/${runtime_file}"
  done
else
  printf 'Preparing credential-isolated runtime\n'
  producer_config_path="${producer_directory}/config.test-pattern.h264.twcc-gcc.yaml"
  if [[ "${flexfec_enabled}" == "true" ]]; then
    producer_config_path="${producer_directory}/config.test-pattern.h264.twcc-gcc-flexfec.yaml"
  fi
  prepare_context_arguments=(
    -context "${context_name}"
    "-flex-fec=${flexfec_enabled}"
    "-flex-fec-media-packets=${flexfec_media_packets}"
    "-flex-fec-repair-packets=${flexfec_repair_packets}"
    -producer-config "${producer_config_path}"
    -producer-turn-policy "${producer_turn_policy}"
    -turn-transport "${turn_transport}"
    -output-directory "${runtime_directory}"
  )
  if [[ -n "${prepare_context_binary}" ]]; then
    "${prepare_context_binary}" "${prepare_context_arguments[@]}"
  else
    go -C "${producer_directory}" run \
      ./qualification/adaptive-streaming/cmd/prepare-context \
      "${prepare_context_arguments[@]}"
  fi
fi
record_setup_milestone runtime-prepared

effective_config_path="${runtime_directory}/${path_kind}-config.yaml"
initial_bitrate_kbps="$(sed -nE 's/^[[:space:]]*initialBitrateKbps:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' "${effective_config_path}")"
minimum_bitrate_kbps="$(sed -nE 's/^[[:space:]]*minBitrateKbps:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' "${effective_config_path}")"
maximum_bitrate_kbps="$(sed -nE 's/^[[:space:]]*maxBitrateKbps:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' "${effective_config_path}")"
change_threshold_pct="$(sed -nE 's/^[[:space:]]*changeThresholdPct:[[:space:]]*([0-9]+)[[:space:]]*$/\1/p' "${effective_config_path}")"
for adaptive_value in \
  "${initial_bitrate_kbps}" \
  "${minimum_bitrate_kbps}" \
  "${maximum_bitrate_kbps}" \
  "${change_threshold_pct}"; do
  if ! [[ "${adaptive_value}" =~ ^[0-9]+$ ]]; then
    printf 'qualification runtime is missing one adaptive bitrate bound in %s\n' \
      "${effective_config_path}" >&2
    exit 1
  fi
done
if ((minimum_bitrate_kbps <= 0 || initial_bitrate_kbps < minimum_bitrate_kbps || maximum_bitrate_kbps < initial_bitrate_kbps || change_threshold_pct > 50)); then
  printf 'qualification runtime has an invalid adaptive bitrate envelope in %s\n' \
    "${effective_config_path}" >&2
  exit 1
fi
recovery_capacity_kbps=$((maximum_bitrate_kbps * 20))

printf 'Building qualification producer image %s\n' "${image_tag}"
record_setup_milestone producer-build-started
pull_arguments=()
case "${docker_pull}" in
always)
  pull_arguments=(--pull)
  ;;
never)
  ;;
*)
  printf 'RSTREAM_QUALIFICATION_DOCKER_PULL must be always or never\n' >&2
  exit 1
  ;;
esac
build_status=1
for attempt in 1 2 3; do
  if producer_docker build "${pull_arguments[@]}" \
    --file "${script_directory}/Dockerfile" \
    --tag "${image_tag}" \
    "${producer_directory}"; then
    build_status=0
    break
  fi
  if ((attempt < 3)); then
    retry_delay=$((attempt * 2))
    printf 'Docker build attempt %s failed; retrying in %ss\n' \
      "${attempt}" "${retry_delay}" >&2
    sleep "${retry_delay}"
  fi
done
if ((build_status != 0)); then
  printf 'Docker build failed after 3 attempts\n' >&2
  exit "${build_status}"
fi
image_id="$(producer_docker image inspect --format '{{.Id}}' "${image_tag}")"
producer_docker_info="$(producer_docker info --format '{{json .}}')"
producer_location="local-docker-daemon"
if producer_is_remote; then
  producer_location="separate-docker-daemon"
fi
record_setup_milestone producer-build-completed

dirty=false
if [[ -n "$(git -C "${repository_directory}" status --porcelain=v1 -- webrtc-video/producer)" ]]; then
  dirty=true
fi
jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg revision "${revision}" \
  --arg producer_tree "${producer_tree}" \
  --argjson dirty "${dirty}" \
  --arg image "${image_id}" \
  --arg path_kind "${path_kind}" \
  --arg protection_profile "${protection_profile}" \
  --arg turn_transport "${turn_transport}" \
  --arg producer_turn_policy "${producer_turn_policy}" \
  --argjson flexfec_enabled "${flexfec_enabled}" \
  --argjson flexfec_media_packets "${flexfec_media_packets}" \
  --argjson flexfec_repair_packets "${flexfec_repair_packets}" \
  --arg docker_version "$(jq -r '.ServerVersion' <<<"${producer_docker_info}")" \
  --arg operating_system "$(jq -r '.OSType' <<<"${producer_docker_info}")" \
  --arg kernel_release "$(jq -r '.KernelVersion' <<<"${producer_docker_info}")" \
  --arg architecture "$(jq -r '.Architecture' <<<"${producer_docker_info}")" \
  --argjson logical_cpus "$(jq -r '.NCPU' <<<"${producer_docker_info}")" \
  --argjson memory_bytes "$(jq -r '.MemTotal' <<<"${producer_docker_info}")" \
  --arg producer_location "${producer_location}" \
  --argjson warmup "${warmup_seconds}" \
  --argjson baseline "${baseline_seconds}" \
  --argjson constrained "${constrained_seconds}" \
  --argjson impaired "${impaired_seconds}" \
  --argjson recovery "${recovery_seconds}" \
  --argjson mobility_seconds "${mobility_seconds}" \
  --arg mobility_mode "${mobility_mode}" \
  --argjson transition_step "${transition_step_seconds}" \
  --argjson capacity "${capacity_kbps}" \
  --argjson queue_limit "${queue_limit_packets}" \
  --argjson capacity_step_one "${capacity_step_one_kbps}" \
  --argjson capacity_step_two "${capacity_step_two_kbps}" \
  --argjson capacity_step_three "${capacity_step_three_kbps}" \
  --argjson recovery_capacity "${recovery_capacity_kbps}" \
  --argjson playout_delay_hint "${playout_delay_hint_seconds}" \
  --argjson initial_bitrate "${initial_bitrate_kbps}" \
  --argjson minimum_bitrate "${minimum_bitrate_kbps}" \
  --argjson maximum_bitrate "${maximum_bitrate_kbps}" \
  --argjson change_threshold "${change_threshold_pct}" \
  '{
    generatedAt: $generated_at,
    git: {revision: $revision, producerTree: $producer_tree, dirty: $dirty},
    producerImage: $image,
    protection: {
      profile: $protection_profile,
      nack: true,
      rtx: true,
      flexFEC: $flexfec_enabled,
      flexFECMediaPackets: (if $flexfec_enabled then $flexfec_media_packets else 0 end),
      flexFECRepairPackets: (if $flexfec_enabled then $flexfec_repair_packets else 0 end)
    },
    networkPath: {
      kind: $path_kind,
      icePolicy: $path_kind,
      description: (if $path_kind == "relay" then "rstream managed TURN over the published producer tunnel" else "direct WebRTC over an isolated Docker bridge" end),
      turnTransport: (if $turn_transport == "" then null else $turn_transport end),
      producerTURNPolicy: $producer_turn_policy
    },
    runtime: {
      dockerVersion: $docker_version,
      operatingSystem: $operating_system,
      kernelRelease: $kernel_release,
      architecture: $architecture,
      logicalCPUs: $logical_cpus,
      memoryBytes: $memory_bytes,
      producerLocation: $producer_location
    },
    video: {
      codec: "H264",
      width: 1920,
      height: 1080,
      framesPerSecond: 30,
      playoutDelayHintSeconds: $playout_delay_hint,
      adaptive: {
        initialBitrateKbps: $initial_bitrate,
        minimumBitrateKbps: $minimum_bitrate,
        maximumBitrateKbps: $maximum_bitrate,
        changeThresholdPct: $change_threshold
      }
    },
    networkMobility: (if $mobility_mode == "producer" then {
      subject: "producer",
      change: "network-interface-and-source-address",
      signalingTransport: "quic",
      durationSeconds: $mobility_seconds
    } else null end),
    phases: ([
      {name: "warmup", durationSeconds: $warmup, shaping: null},
      {name: "baseline", durationSeconds: $baseline, shaping: null}
    ] + (if $mobility_mode == "producer" then [
      {name: "mobility", durationSeconds: $mobility_seconds, shaping: null}
    ] else [] end) + [
      {name: "constrained", durationSeconds: $constrained, shaping: {discipline: "selective-prio+netem", scope: (if $path_kind == "relay" then "producer-turn-transport" else "peer-webrtc-transport-address" end), capacityKbps: $capacity, queueLimitPackets: $queue_limit, loss: "0%", schedule: [{durationSeconds: $transition_step, capacityKbps: $capacity_step_one, delay: "40ms", jitter: "10ms"}, {durationSeconds: $transition_step, capacityKbps: $capacity_step_two, delay: "50ms", jitter: "10ms"}, {durationSeconds: $transition_step, capacityKbps: $capacity_step_three, delay: "65ms", jitter: "15ms"}, {durationSeconds: ($constrained - 3 * $transition_step), capacityKbps: $capacity, delay: "80ms", jitter: "20ms"}]}},
      {name: "impaired", durationSeconds: $impaired, shaping: {discipline: "selective-prio+netem", scope: (if $path_kind == "relay" then "producer-turn-transport" else "peer-webrtc-transport-address" end), capacityKbps: $capacity, queueLimitPackets: $queue_limit, delay: "120ms", jitter: "30ms", loss: "2%"}},
      {name: "recovery", durationSeconds: $recovery, shaping: {discipline: "selective-prio+netem", scope: (if $path_kind == "relay" then "producer-turn-transport" else "peer-webrtc-transport-address" end), capacityKbps: $recovery_capacity, queueLimitPackets: $queue_limit, delay: "0ms", jitter: "0ms", loss: "0%", purpose: "drain in-flight impaired packets before traffic-control teardown"}}
    ])
  }' >"${output_directory}/manifest.json"

printf 'Building the isolated qualification browser\n'
record_setup_milestone browser-build-started
docker build "${pull_arguments[@]}" \
  --file "${script_directory}/Browser.Dockerfile" \
  --tag "${browser_image_tag}" \
  "${script_directory}"
browser_image_id="$(docker image inspect --format '{{.Id}}' "${browser_image_tag}")"
browser_docker_info="$(docker info --format '{{json .}}')"
manifest_temporary="${output_directory}/manifest.json.tmp"
jq --arg browser_image "${browser_image_id}" \
  --arg docker_version "$(jq -r '.ServerVersion' <<<"${browser_docker_info}")" \
  --arg operating_system "$(jq -r '.OSType' <<<"${browser_docker_info}")" \
  --arg kernel_release "$(jq -r '.KernelVersion' <<<"${browser_docker_info}")" \
  --arg architecture "$(jq -r '.Architecture' <<<"${browser_docker_info}")" \
  --argjson logical_cpus "$(jq -r '.NCPU' <<<"${browser_docker_info}")" \
  --argjson memory_bytes "$(jq -r '.MemTotal' <<<"${browser_docker_info}")" \
  '.browserImage = $browser_image | .browserRuntime = {
    dockerVersion: $docker_version,
    operatingSystem: $operating_system,
    kernelRelease: $kernel_release,
    architecture: $architecture,
    logicalCPUs: $logical_cpus,
    memoryBytes: $memory_bytes
  }' \
  "${output_directory}/manifest.json" >"${manifest_temporary}"
mv "${manifest_temporary}" "${output_directory}/manifest.json"
record_setup_milestone browser-build-completed

printf 'Starting isolated producer container %s\n' "${container_name}"
record_setup_milestone connection-started
write_phase connecting '{}'
producer_arguments=()
producer_command_arguments=()
if [[ "${encoder_debug}" == "true" ]]; then
  producer_arguments+=(
    --env GST_DEBUG_NO_COLOR=1
    --env GST_DEBUG=x264enc:6
    --env GST_DEBUG_FILE=/tmp/rstream-qualification-encoder.log
  )
else
  encoder_evidence_captured=1
fi
if [[ -n "${pion_log_debug}" ]]; then
  producer_arguments+=(--env "PION_LOG_DEBUG=${pion_log_debug}")
fi
if [[ "${path_kind}" == "relay" ]]; then
  if [[ "${mobility_mode}" == "producer" ]]; then
    producer_docker network create --driver bridge \
      "${producer_primary_network}" >/dev/null
    producer_primary_network_created=1
    producer_docker network create --driver bridge \
      "${producer_secondary_network}" >/dev/null
    producer_secondary_network_created=1
    producer_arguments+=(--network "${producer_primary_network}")
    producer_arguments+=(--env RSTREAM_TUNNEL_TRANSPORT=quic)
  fi
  if producer_is_remote; then
    producer_docker volume create "${producer_runtime_volume}" >/dev/null
    producer_runtime_volume_created=1
    producer_docker create \
      --name "${producer_seed_container}" \
      --entrypoint sh \
      --mount "type=volume,source=${producer_runtime_volume},target=/runtime" \
      "${image_tag}" -c true >/dev/null
    producer_seed_container_created=1
    producer_docker cp \
      "${runtime_directory}/config.yaml" \
      "${producer_seed_container}:/runtime/config.yaml"
    producer_docker cp \
      "${runtime_directory}/relay-config.yaml" \
      "${producer_seed_container}:/runtime/relay-config.yaml"
    producer_docker rm "${producer_seed_container}" >/dev/null
    producer_seed_container_created=0
    producer_arguments+=(
      --env-file "${runtime_directory}/runtime.env"
      --env RSTREAM_CONFIG=/runtime/config.yaml
      --env RSTREAM_CONTEXT=qualification
      --mount "type=volume,source=${producer_runtime_volume},target=/runtime,readonly"
    )
    producer_command_arguments=(-config /runtime/relay-config.yaml)
  else
    producer_arguments+=(
      --env-file "${runtime_directory}/runtime.env"
      --env RSTREAM_CONFIG=/run/rstream/config.yaml
      --env RSTREAM_CONTEXT=qualification
      --mount "type=bind,source=${runtime_directory}/config.yaml,target=/run/rstream/config.yaml,readonly"
      --mount "type=bind,source=${runtime_directory}/relay-config.yaml,target=/app/config.yaml,readonly"
    )
  fi
else
  docker network create --driver bridge "${network_name}" >/dev/null
  network_created=1
  producer_arguments+=(
    --network "${network_name}"
    --network-alias producer
    --mount "type=bind,source=${runtime_directory}/direct-config.yaml,target=/app/config.yaml,readonly"
  )
fi
producer_docker run --detach \
  --name "${container_name}" \
  --user 0:0 \
  --cap-add NET_ADMIN \
  --security-opt no-new-privileges \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --label rstream.qualification=adaptive-streaming \
  "${producer_arguments[@]}" \
  "${image_tag}" \
  "${producer_command_arguments[@]}" >/dev/null
container_started=1
record_setup_milestone producer-container-started
start_host_cpu_sampler
producer_docker logs --timestamps --follow "${container_name}" \
  >"${output_directory}/producer.log" 2>&1 &
log_pid=$!

target_url=""
for _ in $(seq 1 90); do
  if ! producer_docker inspect --format '{{.State.Running}}' "${container_name}" | grep -qx true; then
    printf 'producer container exited before becoming ready\n' >&2
    producer_docker logs "${container_name}" >&2
    exit 1
  fi
  if [[ "${path_kind}" == "relay" ]]; then
    target_url="$(sed -nE 's/.*Public URL: (https:\/\/[^[:space:]]+).*/\1/p' "${output_directory}/producer.log" | tail -1)"
  elif grep -q 'Local URL:' "${output_directory}/producer.log"; then
    target_url="http://producer:8080"
  fi
  if [[ -n "${target_url}" ]]; then
    break
  fi
  sleep 1
done
if [[ -z "${target_url}" ]]; then
  printf 'producer did not become ready within 90 seconds\n' >&2
  exit 1
fi
record_setup_milestone producer-ready

browser_arguments=(
  --name "${browser_container_name}"
  --user "${browser_user}"
  --security-opt no-new-privileges
  --read-only
  --tmpfs "/tmp:rw,nosuid,size=512m"
  --shm-size 256m
  --env HOME=/tmp
  --mount "type=bind,source=${output_directory},target=/artifacts"
  --mount "type=bind,source=${runtime_directory},target=/runtime,readonly"
  --label rstream.qualification=adaptive-streaming-browser
)
if [[ "${path_kind}" == "direct" ]]; then
  browser_arguments+=(--network "${network_name}")
  printf 'Connecting a direct browser session on the isolated bridge\n'
else
  printf 'Connecting a relay-only browser session in the isolated runtime\n'
fi
docker run \
  "${browser_arguments[@]}" \
  "${browser_image_tag}" \
  --url "${target_url}" \
  --output-directory /artifacts \
  --phase-file /runtime/phase.json \
  --ice-policy "${path_kind}" \
  --browser-executable /usr/bin/chromium \
  --browser-sandbox disabled \
  --playout-delay-hint-seconds "${playout_delay_hint_seconds}" \
  --maximum-duration-seconds 300 \
  >"${output_directory}/browser.log" 2>&1 &
browser_container_started=1
collector_pid=$!
record_setup_milestone browser-container-started
wait_for_file "${output_directory}/collector-ready.json" 120
start_receiver_host_cpu_sampler
wait_for_file "${output_directory}/receiver-host-cpu.jsonl" 10
record_setup_milestone media-connected
start_udp_samplers
if [[ "${path_kind}" == "direct" ]]; then
  direct_address="$(docker inspect --format \
    "{{with index .NetworkSettings.Networks \"${network_name}\"}}{{.IPAddress}}{{end}}" \
    "${browser_container_name}")"
  direct_port="$(jq -r '.mediaDestinationPort // 0' \
    "${output_directory}/collector-ready.json")"
  direct_protocol="$(jq -r '.mediaDestinationProtocol // empty' \
    "${output_directory}/collector-ready.json")"
  impairment_target="$(jq -cn \
    --arg address "${direct_address}" \
    --argjson port "${direct_port}" \
    --arg protocol "${direct_protocol}" \
    '{address: $address, family: 4, port: $port, protocol: $protocol, matchDestinationPort: false, relayProtocol: null, scope: "peer-webrtc-transport-address", source: "docker-bridge"}')"
else
  impairment_target="$(node "${script_directory}/resolve-impairment-target.mjs" \
    "${output_directory}/collector-ready.json" "${path_kind}")"
fi
media_destination_address="$(jq -r '.address' <<<"${impairment_target}")"
media_destination_family="$(jq -r '.family' <<<"${impairment_target}")"
media_destination_port="$(jq -r '.port' <<<"${impairment_target}")"
media_destination_protocol="$(jq -r '.protocol' <<<"${impairment_target}")"
match_destination_port="$(jq -r '.matchDestinationPort' <<<"${impairment_target}")"
impairment_scope="$(jq -r '.scope' <<<"${impairment_target}")"
if ! positive_integer "${media_destination_port}" || \
  ((media_destination_port > 65535)); then
  printf 'collector returned an invalid media destination port: %s\n' \
    "${media_destination_port}" >&2
  exit 1
fi
if ! detected_destination_family="$(node -e '
  const {isIP} = require("node:net");
  const family = isIP(process.argv[1]);
  if (family === 0) process.exit(1);
  process.stdout.write(String(family));
' "${media_destination_address}")"; then
  printf 'collector returned an invalid media destination address: %s\n' \
    "${media_destination_address}" >&2
  exit 1
fi
if [[ "${detected_destination_family}" != "${media_destination_family}" ]]; then
  printf 'impairment target family %s does not match address %s\n' \
    "${media_destination_family}" "${media_destination_address}" >&2
  exit 1
fi
case "${media_destination_protocol}" in
udp | tcp)
  ;;
*)
  printf 'unsupported impairment target protocol: %s\n' \
    "${media_destination_protocol}" >&2
  exit 1
  ;;
esac
case "${match_destination_port}" in
true | false)
  ;;
*)
  printf 'impairment target returned invalid matchDestinationPort: %s\n' \
    "${match_destination_port}" >&2
  exit 1
  ;;
esac
manifest_temporary="${output_directory}/manifest.json.tmp"
jq --slurpfile browser "${output_directory}/browser.json" \
  --arg path_kind "${path_kind}" \
  --arg media_destination_address "${media_destination_address}" \
  --argjson media_destination_family "${media_destination_family}" \
  --argjson media_destination_port "${media_destination_port}" \
  --arg media_destination_protocol "${media_destination_protocol}" \
  --argjson match_destination_port "${match_destination_port}" \
  --arg impairment_scope "${impairment_scope}" \
  --argjson impairment_target "${impairment_target}" \
  '.webrtc = {
    codecs: $browser[0].peerConnection.codecs,
    flexFECNegotiated: $browser[0].peerConnection.flexFECNegotiated,
    rtxNegotiated: $browser[0].peerConnection.rtxNegotiated,
    transceivers: $browser[0].peerConnection.transceivers
  } | .networkImpairment = {
    direction: "producer-to-viewer",
    scope: $impairment_scope,
    resolvedTarget: $impairment_target,
    destination: {
      address: $media_destination_address,
      family: $media_destination_family,
      port: $media_destination_port,
      protocol: $media_destination_protocol
    },
    matchDestinationPort: $match_destination_port
  }' "${output_directory}/manifest.json" >"${manifest_temporary}"
mv "${manifest_temporary}" "${output_directory}/manifest.json"

run_phase warmup "${warmup_seconds}" '{}'
run_phase baseline "${baseline_seconds}" '{}'
if [[ "${mobility_mode}" == "producer" ]]; then
  write_phase mobility \
    '{"subject":"producer","change":"network-interface-and-source-address"}'
  producer_before_address="$(producer_docker inspect --format \
    "{{with index .NetworkSettings.Networks \"${producer_primary_network}\"}}{{.IPAddress}}{{end}}" \
    "${container_name}")"
  producer_docker network connect \
    "${producer_secondary_network}" "${container_name}"
  producer_docker network disconnect \
    "${producer_primary_network}" "${container_name}"
  producer_after_address="$(producer_docker inspect --format \
    "{{with index .NetworkSettings.Networks \"${producer_secondary_network}\"}}{{.IPAddress}}{{end}}" \
    "${container_name}")"
  if [[ -z "${producer_before_address}" || \
    -z "${producer_after_address}" || \
    "${producer_before_address}" == "${producer_after_address}" ]]; then
    printf 'producer mobility did not establish a distinct source address\n' >&2
    exit 1
  fi
  manifest_temporary="${output_directory}/manifest.json.tmp"
  jq --arg before_address "${producer_before_address}" \
    --arg after_address "${producer_after_address}" \
    '.networkMobility.beforeAddress = $before_address |
      .networkMobility.afterAddress = $after_address |
      .networkMobility.addressChanged = ($before_address != $after_address)' \
    "${output_directory}/manifest.json" >"${manifest_temporary}"
  mv "${manifest_temporary}" "${output_directory}/manifest.json"
  hold_phase mobility "${mobility_seconds}"
fi
start_traffic_control
wait_for_traffic_control_event constrained-started 15
write_phase constrained \
  "$(jq -cn --argjson one "${capacity_step_one_kbps}" --argjson two "${capacity_step_two_kbps}" --argjson three "${capacity_step_three_kbps}" --argjson steady "${capacity_kbps}" --argjson queue_limit "${queue_limit_packets}" --arg scope "${impairment_scope}" '{schedule: [{capacityKbps: $one, delay: "40ms", jitter: "10ms"}, {capacityKbps: $two, delay: "50ms", jitter: "10ms"}, {capacityKbps: $three, delay: "65ms", jitter: "15ms"}, {capacityKbps: $steady, delay: "80ms", jitter: "20ms"}], loss: "0%", queueLimitPackets: $queue_limit, scope: $scope}')"
wait_for_traffic_control_event impaired-started $((constrained_seconds + 15))
write_phase impaired \
  "$(jq -cn --argjson capacity "${capacity_kbps}" --argjson queue_limit "${queue_limit_packets}" --arg scope "${impairment_scope}" '{capacityKbps: $capacity, delay: "120ms", jitter: "30ms", loss: "2%", queueLimitPackets: $queue_limit, scope: $scope}')"
wait_for_traffic_control_event recovery-started $((impaired_seconds + 15))
write_phase recovery \
  "$(jq -cn --argjson capacity "${recovery_capacity_kbps}" --argjson queue_limit "${queue_limit_packets}" --arg scope "${impairment_scope}" '{capacityKbps: $capacity, delay: "0ms", jitter: "0ms", loss: "0%", queueLimitPackets: $queue_limit, scope: $scope, purpose: "drain in-flight impaired packets before traffic-control teardown"}')"
hold_phase recovery "${recovery_seconds}"
wait_for_traffic_control
# The collector exits as soon as it observes the complete phase. Stop the
# namespace sampler first so Docker cannot terminate it while its container is
# exiting and turn a successful qualification into an unanalysed status 137.
stop_udp_samplers
if ! stop_receiver_host_cpu_sampler; then
  printf 'receiver host CPU sampler did not stop cleanly\n' >&2
  exit 1
fi
write_phase complete '{}'
wait "${collector_pid}"
collector_pid=""
if ! stop_host_cpu_sampler; then
  printf 'producer host CPU sampler did not stop cleanly\n' >&2
  exit 1
fi
if ! capture_host_cpu_evidence; then
  printf 'failed to copy producer host CPU evidence from the producer tmpfs\n' >&2
  exit 1
fi
if ! capture_network_evidence; then
  printf 'failed to copy traffic-control evidence from the producer tmpfs\n' >&2
  exit 1
fi
kill -TERM "${log_pid}" 2>/dev/null || true
wait "${log_pid}" 2>/dev/null || true
log_pid=""
if [[ "${encoder_debug}" == "true" ]]; then
  media_stopped=0
  for _ in $(seq 1 50); do
    if producer_docker logs "${container_name}" 2>&1 | grep -q 'GStreamer pipeline stopped'; then
      media_stopped=1
      break
    fi
    sleep 0.1
  done
  if ((media_stopped != 1)); then
    printf 'producer media pipeline did not stop after the browser disconnected\n' >&2
    exit 1
  fi
  if ! capture_encoder_evidence; then
    printf 'failed to copy encoder evidence from the producer tmpfs\n' >&2
    exit 1
  fi
fi
if ! producer_docker stop --time 5 "${container_name}" >/dev/null; then
  printf 'producer did not stop cleanly after qualification\n' >&2
  exit 1
fi
producer_docker logs --timestamps "${container_name}" \
  >"${output_directory}/producer.log" 2>&1

printf 'Analyzing qualification evidence\n'
node "${script_directory}/lib/analysis.mjs" "${output_directory}"
if grep -ERq 'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}' "${output_directory}"; then
  printf 'refusing to retain artifacts that appear to contain a token\n' >&2
  exit 1
fi
printf 'Qualification artifacts: %s\n' "${output_directory}"
