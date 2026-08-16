#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_directory}/common.sh"

output_directory="$(prepare_output_directory "${1:?output directory is required}")"
go_binary="$(resolve_binary "${RSTREAM_GO_BIN:-}" rstream)"
cpp_binary="$(resolve_binary "${RSTREAM_CPP_BIN:-}" rstream-ncat)"
timeout_binary="$(resolve_timeout)"
for command in ffmpeg gst-launch-1.0 shasum; do
  require_command "${command}"
done
validate_positive_integer RSTREAM_MEDIA_FRAMES "${media_frames}"
write_manifest "${output_directory}" reliable-media-matrix "${go_binary}" "${cpp_binary}"

server_pid=""

start_server() {
  local implementation="$1"
  local tunnel_name="$2"
  local media_command="$3"
  local log_path="$4"
  if [[ "${implementation}" == go ]]; then
    "${context_environment[@]}" "${go_binary}" nc -L "rstrm://${tunnel_name}" \
      -c "${media_command}" >"${log_path}" 2>&1 &
  else
    "${context_environment[@]}" "${cpp_binary}" --buffer-size=16 \
      -L "rstrm://${tunnel_name}?rstrm.publish=false&rstrm.protocol=tls" \
      -c "${media_command}" --jobs=4 >"${log_path}" 2>&1 &
  fi
  server_pid=$!
  register_pid "${server_pid}"
  sleep 3
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    wait "${server_pid}" || true
    forget_pid "${server_pid}"
    server_pid=""
    return 1
  fi
}

stop_server() {
  local status=0
  stop_pid "${server_pid}" INT || status=$?
  server_pid=""
  if ((status != 0)); then
    printf 'server did not shut down cleanly: exit %s\n' "${status}" >&2
    return 1
  fi
}

run_client() {
  local implementation="$1"
  local tunnel_name="$2"
  if [[ "${implementation}" == go ]]; then
    "${context_environment[@]}" "${go_binary}" nc "rstrm://${tunnel_name}"
	else
		"${context_environment[@]}" "${cpp_binary}" --buffer-size=16 \
			"rstrm://${tunnel_name}" -I --jobs=4
	fi
}

run_ffmpeg_case() {
  local server_implementation="$1"
  local client_implementation="$2"
  local label="ffmpeg-${server_implementation}-to-${client_implementation}"
  local tunnel_name
  tunnel_name="netcat-${label}-$(date +%s)-$$"
  local case_directory="${output_directory}/${label}"
  mkdir -p "${case_directory}"
  local output_path="${case_directory}/decoded.i420"
  local quoted_case_directory
  quoted_case_directory="$(shell_quote "${case_directory}")"
  local media_command
  media_command="cd ${quoted_case_directory} || exit 1; exec 3>&1 1>producer.stdout 2>producer.stderr; exec ffmpeg -hide_banner -loglevel warning -re -f lavfi -i testsrc2=size=${media_width}x${media_height}:rate=30 -frames:v ${media_frames} -c:v libx264 -preset veryfast -tune zerolatency -b:v 500k -g 60 -f mpegts pipe:3"
  if ! start_server "${server_implementation}" "${tunnel_name}" \
    "${media_command}" "${case_directory}/server.log"; then
    printf 'FAIL %s server startup\n' "${label}" | tee "${case_directory}/result.txt"
    return 1
  fi
  local pipeline_status=0
  run_client "${client_implementation}" "${tunnel_name}" \
    2>"${case_directory}/client.log" |
    "${timeout_binary}" 120 ffmpeg -hide_banner -loglevel error \
      -probesize 32 -analyzeduration 0 -flags low_delay -i pipe:0 \
      -map 0:v:0 -fps_mode passthrough -pix_fmt yuv420p \
      -f rawvideo "${output_path}" 2>"${case_directory}/decoder.log" ||
    pipeline_status=$?
  local shutdown_status=0
  stop_server || shutdown_status=$?
  if ((pipeline_status != 0 || shutdown_status != 0)); then
    printf 'FAIL %s pipeline=%s shutdown=%s\n' \
      "${label}" "${pipeline_status}" "${shutdown_status}" |
      tee "${case_directory}/result.txt"
    return 1
  fi
  assert_exact_frames "${label}" "${output_path}" \
    "${case_directory}/result.txt"
}

run_gstreamer_case() {
  local server_implementation="$1"
  local client_implementation="$2"
  local label="gstreamer-${server_implementation}-to-${client_implementation}"
  local tunnel_name
  tunnel_name="netcat-${label}-$(date +%s)-$$"
  local case_directory="${output_directory}/${label}"
  mkdir -p "${case_directory}"
  local output_path="${case_directory}/decoded.i420"
  local quoted_case_directory
  quoted_case_directory="$(shell_quote "${case_directory}")"
  local media_command
  media_command="cd ${quoted_case_directory} || exit 1; exec 3>&1 1>producer.stdout 2>producer.stderr; exec env GST_DEBUG_NO_COLOR=1 GST_DEBUG='*:2' gst-launch-1.0 -q --no-position videotestsrc num-buffers=${media_frames} is-live=true pattern=smpte ! video/x-raw,width=${media_width},height=${media_height},framerate=30/1 ! videoconvert ! x264enc tune=zerolatency bitrate=500 key-int-max=60 ! h264parse config-interval=-1 ! mpegtsmux alignment=7 ! fdsink fd=3 sync=false"
  if ! start_server "${server_implementation}" "${tunnel_name}" \
    "${media_command}" "${case_directory}/server.log"; then
    printf 'FAIL %s server startup\n' "${label}" | tee "${case_directory}/result.txt"
    return 1
  fi
  local pipeline_status=0
  run_client "${client_implementation}" "${tunnel_name}" \
    2>"${case_directory}/client.log" |
    "${timeout_binary}" 120 env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
      gst-launch-1.0 -q --no-position fdsrc fd=0 ! tsdemux name=demux \
      demux. ! queue ! decodebin ! videoconvert ! \
      "video/x-raw,format=I420,width=${media_width},height=${media_height}" ! \
      fdsink fd=1 sync=false >"${output_path}" \
      2>"${case_directory}/decoder.log" || pipeline_status=$?
  local shutdown_status=0
  stop_server || shutdown_status=$?
  if ((pipeline_status != 0 || shutdown_status != 0)); then
    printf 'FAIL %s pipeline=%s shutdown=%s\n' \
      "${label}" "${pipeline_status}" "${shutdown_status}" |
      tee "${case_directory}/result.txt"
    return 1
  fi
  assert_exact_frames "${label}" "${output_path}" \
    "${case_directory}/result.txt"
}

failures=0
run_ffmpeg_case go cpp || failures=$((failures + 1))
run_ffmpeg_case cpp go || failures=$((failures + 1))
run_gstreamer_case go cpp || failures=$((failures + 1))
run_gstreamer_case cpp go || failures=$((failures + 1))

if ((failures > 0)); then
  printf 'FAIL reliable media matrix: %s/4 scenarios failed\n' "${failures}" |
    tee "${output_directory}/summary.txt"
  exit 1
fi
printf 'PASS reliable media matrix: 4/4 scenarios passed\n' |
  tee "${output_directory}/summary.txt"
