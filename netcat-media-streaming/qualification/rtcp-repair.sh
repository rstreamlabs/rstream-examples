#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_directory}/common.sh"

output_directory="$(prepare_output_directory "${1:?output directory is required}")"
go_binary="$(resolve_binary "${RSTREAM_GO_BIN:-}" rstream)"
drop_probability="${RSTREAM_RTP_DROP_PROBABILITY:-0.01}"
minimum_delivery_percent="${RSTREAM_RTCP_MIN_FRAME_DELIVERY_PERCENT:-100}"
repair_tail_frames="${RSTREAM_RTCP_REPAIR_TAIL_FRAMES:-60}"
for command in awk grep gst-launch-1.0 shasum; do
  require_command "${command}"
done
validate_positive_integer RSTREAM_MEDIA_FRAMES "${media_frames}"
validate_positive_integer RSTREAM_RTCP_MIN_FRAME_DELIVERY_PERCENT \
	"${minimum_delivery_percent}"
validate_positive_integer RSTREAM_RTCP_REPAIR_TAIL_FRAMES \
	"${repair_tail_frames}"
if ((minimum_delivery_percent > 100)); then
	printf 'RSTREAM_RTCP_MIN_FRAME_DELIVERY_PERCENT must not exceed 100\n' >&2
	exit 1
fi
if ! awk 'BEGIN { value = ARGV[1] + 0; exit !(ARGV[1] ~ /^[0-9.]+$/ && value > 0 && value < 1) }' \
  "${drop_probability}"; then
  printf 'RSTREAM_RTP_DROP_PROBABILITY must be between 0 and 1\n' >&2
  exit 1
fi
write_manifest "${output_directory}" rtcp-repair "${go_binary}"
producer_frames=$((media_frames + repair_tail_frames))
eos_after_frames=$((media_frames + 1))
jq --argjson producer_frames "${producer_frames}" \
	--argjson target_frames "${media_frames}" \
	--argjson repair_tail_frames "${repair_tail_frames}" \
	--argjson minimum_delivery_percent "${minimum_delivery_percent}" \
	--arg drop_probability "${drop_probability}" \
	'.rtcp = {
		producerFrames: $producer_frames,
		targetFrames: $target_frames,
		repairTailFrames: $repair_tail_frames,
		minimumDeliveryPercent: $minimum_delivery_percent,
		dropProbability: ($drop_probability | tonumber)
	}' "${output_directory}/manifest.json" >"${output_directory}/manifest.tmp"
mv "${output_directory}/manifest.tmp" "${output_directory}/manifest.json"
minimum_frames=$(((media_frames * minimum_delivery_percent + 99) / 100))
minimum_bytes=$((minimum_frames * frame_bytes))

tunnel_name="netcat-rtcp-$(date +%s)-$$"
quoted_output_directory="$(shell_quote "${output_directory}")"
producer_command="cd ${quoted_output_directory} || exit 1; exec 3>&1 1>producer.stdout 2>producer.stderr; exec env GST_DEBUG_NO_COLOR=1 GST_DEBUG='*:2,rtprtxqueue:5' gst-launch-1.0 -q --no-position rtpbin name=rtp rtp-profile=avpf latency=1000 videotestsrc num-buffers=${producer_frames} is-live=true pattern=ball ! video/x-raw,width=${media_width},height=${media_height},framerate=30/1 ! videoconvert ! x264enc tune=zerolatency bitrate=500 key-int-max=60 ! rtph264pay pt=96 mtu=1200 config-interval=1 ! rtprtxqueue max-size-packets=0 max-size-time=15000 ! rtp.send_rtp_sink_0 rtp.send_rtp_src_0 ! identity drop-probability=${drop_probability} ! rtpstreampay ! fdsink fd=3 sync=false fdsrc fd=0 is-live=true ! application/x-rtcp-stream ! rtpstreamdepay ! identity dump=true silent=true ! rtp.recv_rtcp_sink_0"
consumer_command="cd ${quoted_output_directory} || exit 1; exec 3>&1 1>consumer.stdout 2>consumer.stderr; exec env GST_DEBUG_NO_COLOR=1 GST_DEBUG='*:2' gst-launch-1.0 -q --no-position rtpbin name=rtp rtp-profile=avpf buffer-mode=none latency=1000 do-retransmission=true drop-on-latency=true fdsrc fd=0 is-live=true ! application/x-rtp-stream,media=video,clock-rate=90000,encoding-name=H264,payload=96 ! rtpstreamdepay ! rtp.recv_rtp_sink_0 rtp. ! rtph264depay ! h264parse ! avdec_h264 ! videoconvert ! video/x-raw,format=I420,width=${media_width},height=${media_height} ! identity eos-after=${eos_after_frames} ! filesink location=decoded.i420 sync=false rtp.send_rtcp_src_0 ! rtpstreampay ! fdsink fd=3 sync=false"

"${context_environment[@]}" "${go_binary}" nc -u -L "rstrm://${tunnel_name}" \
  -c "${producer_command}" >"${output_directory}/server.log" 2>&1 &
server_pid=$!
register_pid "${server_pid}"
sleep 3
if ! kill -0 "${server_pid}" 2>/dev/null; then
  wait "${server_pid}" || true
  forget_pid "${server_pid}"
  printf 'FAIL RTCP server startup\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

"${context_environment[@]}" "${go_binary}" nc -u "rstrm://${tunnel_name}" \
  -c "${consumer_command}" >"${output_directory}/client.log" 2>&1 &
client_pid=$!
register_pid "${client_pid}"
if ! wait_until_file_size "${output_directory}/decoded.i420" "${minimum_bytes}" 120; then
	printf 'FAIL RTCP timed out before decoding %s/%s frames\n' \
		"${minimum_frames}" "${media_frames}" |
		tee "${output_directory}/summary.txt"
	exit 1
fi
sleep 1
client_status=0
server_status=0
stop_pid "${client_pid}" INT || client_status=$?
client_pid=""
stop_pid "${server_pid}" INT || server_status=$?
server_pid=""
if ((client_status != 0 || server_status != 0)); then
  printf 'FAIL RTCP shutdown client=%s server=%s\n' \
    "${client_status}" "${server_status}" | tee "${output_directory}/summary.txt"
  exit 1
fi

feedback_bytes="$(wc -c <"${output_directory}/producer.stdout" | tr -d ' ')"
request_count="$(grep -c 'rtprtxqueue.*request [0-9]' "${output_directory}/producer.stderr" || true)"
found_count="$(grep -c 'rtprtxqueue.*found [0-9]' "${output_directory}/producer.stderr" || true)"
if ((feedback_bytes == 0)); then
  printf 'FAIL RTCP feedback path produced no receiver reports\n' |
    tee "${output_directory}/summary.txt"
  exit 1
fi
if ((request_count == 0)); then
  printf 'FAIL RTCP injected loss produced no retransmission request\n' |
    tee "${output_directory}/summary.txt"
  exit 1
fi
if ((found_count < request_count)); then
  printf 'FAIL RTCP repair incomplete requests=%s found=%s\n' \
    "${request_count}" "${found_count}" | tee "${output_directory}/summary.txt"
  exit 1
fi
analysis_status=0
python3 "${script_directory}/analyze_frames.py" \
	"${output_directory}/decoded.i420" \
	--frame-bytes "${frame_bytes}" \
	--expected-frames "${media_frames}" \
	--minimum-percent "${minimum_delivery_percent}" \
	--output "${output_directory}/frames.json" \
	>"${output_directory}/frames.txt" || analysis_status=$?
if ((analysis_status != 0)); then
	printf 'FAIL RTCP frame analysis status=%s\n' "${analysis_status}" |
		tee "${output_directory}/summary.txt"
	exit 1
fi
decoded_frames="$(jq -r .decodedFrames "${output_directory}/frames.json")"
delivery_percent="$(jq -r .deliveryPercent "${output_directory}/frames.json")"
printf 'PASS RTCP repair frames=%s/%s delivery=%s%% feedback_bytes=%s requests=%s found=%s loss=%s\n' \
	"${decoded_frames}" "${media_frames}" "${delivery_percent}" \
	"${feedback_bytes}" "${request_count}" "${found_count}" \
	"${drop_probability}" | tee "${output_directory}/summary.txt"
