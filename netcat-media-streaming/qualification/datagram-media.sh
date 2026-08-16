#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_directory}/common.sh"

output_directory="$(prepare_output_directory "${1:?output directory is required}")"
go_binary="$(resolve_binary "${RSTREAM_GO_BIN:-}" rstream)"
timeout_binary="$(resolve_timeout)"
guaranteed_delivery="${RSTREAM_DATAGRAM_GUARANTEED_DELIVERY:-false}"
case "${guaranteed_delivery}" in
true)
  scenario_name=datagram-media-guaranteed
  default_minimum_packet_delivery=100
  default_minimum_identical_frames=100
  ;;
false)
  scenario_name=datagram-media-best-effort
  default_minimum_packet_delivery=99
  default_minimum_identical_frames=90
  ;;
*)
  printf 'RSTREAM_DATAGRAM_GUARANTEED_DELIVERY must be true or false\n' >&2
  exit 1
  ;;
esac
for command in bash gst-launch-1.0 python3 shasum; do
  require_command "${command}"
done
validate_positive_integer RSTREAM_MEDIA_FRAMES "${media_frames}"
write_manifest "${output_directory}" "${scenario_name}" "${go_binary}"
jq --argjson guaranteed_delivery "${guaranteed_delivery}" \
  '.datagram = {guaranteedDelivery: $guaranteed_delivery}' \
  "${output_directory}/manifest.json" >"${output_directory}/manifest.tmp"
mv "${output_directory}/manifest.tmp" "${output_directory}/manifest.json"

tunnel_name="netcat-datagram-$(date +%s)-$$"
quoted_output_directory="$(shell_quote "${output_directory}")"
probe_script="$(shell_quote "${script_directory}/rtp_probe.py")"
producer_metrics="$(shell_quote "${output_directory}/producer-rtp.json")"
producer_pipeline="env GST_DEBUG_NO_COLOR=1 GST_DEBUG='*:2' gst-launch-1.0 -q --no-position videotestsrc num-buffers=${media_frames} is-live=true pattern=ball ! video/x-raw,width=${media_width},height=${media_height},framerate=30/1 ! videoconvert ! x264enc tune=zerolatency bitrate=500 key-int-max=30 ! h264parse config-interval=-1 ! video/x-h264,stream-format=byte-stream,alignment=au ! tee name=encoded encoded. ! queue ! filesink location=reference.h264 sync=false encoded. ! queue ! rtph264pay pt=96 mtu=1200 config-interval=1 ! rtpstreampay ! fdsink fd=1 sync=false | python3 ${probe_script} --output ${producer_metrics}"
quoted_producer_pipeline="$(shell_quote "${producer_pipeline}")"
producer_command="cd ${quoted_output_directory} || exit 1; exec 3>&1 1>producer.stdout 2>producer.stderr; exec bash -o pipefail -c ${quoted_producer_pipeline} >&3"
server_arguments=(nc -u -L "rstrm://${tunnel_name}" -c "${producer_command}")
if [[ "${guaranteed_delivery}" == true ]]; then
  server_arguments+=(--datagram-guaranteed-delivery)
fi
"${context_environment[@]}" "${go_binary}" "${server_arguments[@]}" \
  >"${output_directory}/server.log" 2>&1 &
server_pid=$!
register_pid "${server_pid}"
sleep 3
if ! kill -0 "${server_pid}" 2>/dev/null; then
  wait "${server_pid}" || true
  forget_pid "${server_pid}"
  printf 'FAIL datagram server startup\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

pipeline_status=0
"${context_environment[@]}" "${go_binary}" nc -u --idle-timeout 3s \
  "rstrm://${tunnel_name}" \
  2>"${output_directory}/client.log" |
  python3 "${script_directory}/rtp_probe.py" \
    --output "${output_directory}/receiver-rtp.json" |
  "${timeout_binary}" 120 env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
    gst-launch-1.0 -q --no-position fdsrc fd=0 ! \
    application/x-rtp-stream,media=video,clock-rate=90000,encoding-name=H264,payload=96 ! \
    rtpstreamdepay ! rtpjitterbuffer latency=1000 drop-on-latency=true ! \
    rtph264depay ! h264parse ! avdec_h264 ! videoconvert ! \
    "video/x-raw,format=I420,width=${media_width},height=${media_height}" ! \
    fdsink fd=1 sync=false >"${output_directory}/decoded.i420" \
    2>"${output_directory}/decoder.log" || pipeline_status=$?
shutdown_status=0
stop_pid "${server_pid}" INT || shutdown_status=$?
server_pid=""
if ((pipeline_status != 0 || shutdown_status != 0)); then
  printf 'FAIL datagram media pipeline=%s shutdown=%s\n' \
    "${pipeline_status}" "${shutdown_status}" |
    tee "${output_directory}/summary.txt"
  exit 1
fi
reference_status=0
env GST_DEBUG_NO_COLOR=1 GST_DEBUG="*:2" \
  gst-launch-1.0 -q --no-position \
  filesrc location="${output_directory}/reference.h264" ! h264parse ! \
  avdec_h264 ! videoconvert ! \
  "video/x-raw,format=I420,width=${media_width},height=${media_height}" ! \
  filesink location="${output_directory}/reference.i420" sync=false \
  2>"${output_directory}/reference-decoder.log" || reference_status=$?
quality_status=0
python3 "${script_directory}/compare_frames.py" \
  --reference "${output_directory}/reference.i420" \
  --candidate "${output_directory}/decoded.i420" \
  --frame-bytes "${frame_bytes}" \
  --expected-frames "${media_frames}" \
  --minimum-identical-percent "${RSTREAM_MIN_IDENTICAL_FRAMES_PERCENT:-${default_minimum_identical_frames}}" \
  --output "${output_directory}/frame-comparison.json" \
  --summary "${output_directory}/frame-summary.txt" || quality_status=$?
rtp_status=0
python3 "${script_directory}/analyze_rtp.py" \
  --sender "${output_directory}/producer-rtp.json" \
  --receiver "${output_directory}/receiver-rtp.json" \
  --minimum-delivery-percent "${RSTREAM_MIN_PACKET_DELIVERY_PERCENT:-${default_minimum_packet_delivery}}" \
  --output "${output_directory}/rtp-analysis.json" \
  --summary "${output_directory}/rtp-summary.txt" || rtp_status=$?
cat "${output_directory}/frame-summary.txt" \
  "${output_directory}/rtp-summary.txt" >"${output_directory}/summary.txt"
if ((reference_status != 0 || quality_status != 0 || rtp_status != 0)); then
  exit 1
fi
