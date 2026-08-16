#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_directory}/common.sh"

output_directory="$(prepare_output_directory "${1:?output directory is required}")"
go_binary="$(resolve_binary "${RSTREAM_GO_BIN:-}" rstream)"
cpp_binary="$(resolve_binary "${RSTREAM_CPP_BIN:-}" rstream-ncat)"
timeout_binary="$(resolve_timeout)"
for command in ffmpeg mediamtx nc python3 shasum; do
  require_command "${command}"
done
validate_positive_integer RSTREAM_MEDIA_FRAMES "${media_frames}"
write_manifest "${output_directory}" rtsp-media-matrix "${go_binary}" "${cpp_binary}"

read -r rtsp_port go_port cpp_port < <(python3 - <<'PY'
import socket

sockets = [socket.socket() for _ in range(3)]
try:
    for item in sockets:
        item.bind(("127.0.0.1", 0))
    print(*(item.getsockname()[1] for item in sockets))
finally:
    for item in sockets:
        item.close()
PY
)
tunnel_name="netcat-rtsp-$(date +%s)-$$"

env \
  MTX_RTSPADDRESS=":${rtsp_port}" \
  MTX_LOGLEVEL=warn \
  MTX_HLS=false \
  MTX_WEBRTC=false \
  MTX_SRT=false \
  MTX_RTMP=false \
  MTX_PLAYBACK=false \
  MTX_API=false \
  MTX_METRICS=false \
  MTX_PPROF=false \
  MTX_MOQ=false \
  MTX_PATHS_CAM_SOURCE=publisher \
  mediamtx "${script_directory}/mediamtx.yml" \
  >"${output_directory}/mediamtx.log" 2>&1 &
mediamtx_pid=$!
register_pid "${mediamtx_pid}"
if ! wait_until_listening 127.0.0.1 "${rtsp_port}"; then
  printf 'FAIL MediaMTX readiness\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

ffmpeg -hide_banner -loglevel warning -re -f lavfi \
  -i "testsrc2=size=${media_width}x${media_height}:rate=30" \
  -c:v libx264 -preset veryfast -tune zerolatency -b:v 500k -g 60 \
  -f rtsp -rtsp_transport tcp "rtsp://127.0.0.1:${rtsp_port}/cam" \
  >"${output_directory}/publisher.stdout" \
  2>"${output_directory}/publisher.stderr" &
publisher_pid=$!
register_pid "${publisher_pid}"
sleep 2
if ! kill -0 "${publisher_pid}" 2>/dev/null; then
  printf 'FAIL RTSP publisher startup\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

"${context_environment[@]}" "${go_binary}" forward "127.0.0.1:${rtsp_port}" \
  --bytestream --no-publish --name "${tunnel_name}" --output text \
  >"${output_directory}/forward.log" 2>&1 &
forward_pid=$!
register_pid "${forward_pid}"
sleep 3
if ! kill -0 "${forward_pid}" 2>/dev/null; then
  printf 'FAIL rstream RTSP forward startup\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

"${context_environment[@]}" "${go_binary}" nc -L "127.0.0.1:${go_port}" \
  -R "rstrm://${tunnel_name}" >"${output_directory}/go-bridge.log" 2>&1 &
go_bridge_pid=$!
register_pid "${go_bridge_pid}"
"${context_environment[@]}" "${cpp_binary}" --buffer-size=16 -L "127.0.0.1:${cpp_port}" \
  -R "rstrm://${tunnel_name}" --jobs=4 \
  >"${output_directory}/cpp-bridge.log" 2>&1 &
cpp_bridge_pid=$!
register_pid "${cpp_bridge_pid}"
if ! wait_until_listening 127.0.0.1 "${go_port}" ||
  ! wait_until_listening 127.0.0.1 "${cpp_port}"; then
  printf 'FAIL local RTSP bridge readiness\n' | tee "${output_directory}/summary.txt"
  exit 1
fi

"${timeout_binary}" 180 ffmpeg -hide_banner -loglevel error \
  -rtsp_transport tcp -flags low_delay \
  -i "rtsp://127.0.0.1:${go_port}/cam" -map 0:v:0 \
  -frames:v "${media_frames}" -fps_mode passthrough -pix_fmt yuv420p \
  -f rawvideo "${output_directory}/go.i420" \
  >"${output_directory}/go-decoder.stdout" \
  2>"${output_directory}/go-decoder.stderr" &
go_decoder_pid=$!
register_pid "${go_decoder_pid}"
"${timeout_binary}" 180 ffmpeg -hide_banner -loglevel error \
  -rtsp_transport tcp -flags low_delay \
  -i "rtsp://127.0.0.1:${cpp_port}/cam" -map 0:v:0 \
  -frames:v "${media_frames}" -fps_mode passthrough -pix_fmt yuv420p \
  -f rawvideo "${output_directory}/cpp.i420" \
  >"${output_directory}/cpp-decoder.stdout" \
  2>"${output_directory}/cpp-decoder.stderr" &
cpp_decoder_pid=$!
register_pid "${cpp_decoder_pid}"

go_decoder_status=0
cpp_decoder_status=0
wait "${go_decoder_pid}" || go_decoder_status=$?
forget_pid "${go_decoder_pid}"
go_decoder_pid=""
wait "${cpp_decoder_pid}" || cpp_decoder_status=$?
forget_pid "${cpp_decoder_pid}"
cpp_decoder_pid=""
if ((go_decoder_status != 0 || cpp_decoder_status != 0)); then
  printf 'FAIL RTSP decoder go=%s cpp=%s\n' \
    "${go_decoder_status}" "${cpp_decoder_status}" |
    tee "${output_directory}/summary.txt"
  exit 1
fi

assert_exact_frames rtsp-go-bridge "${output_directory}/go.i420" \
  "${output_directory}/go-result.txt"
assert_exact_frames rtsp-cpp-bridge "${output_directory}/cpp.i420" \
  "${output_directory}/cpp-result.txt"

go_bridge_status=0
cpp_bridge_status=0
forward_status=0
stop_pid "${go_bridge_pid}" INT || go_bridge_status=$?
go_bridge_pid=""
stop_pid "${cpp_bridge_pid}" INT || cpp_bridge_status=$?
cpp_bridge_pid=""
stop_pid "${forward_pid}" INT || forward_status=$?
forward_pid=""
stop_pid "${publisher_pid}" TERM || true
publisher_pid=""
stop_pid "${mediamtx_pid}" TERM || true
mediamtx_pid=""
if ((go_bridge_status != 0 || cpp_bridge_status != 0 || forward_status != 0)); then
  printf 'FAIL RTSP bridge shutdown go=%s cpp=%s forward=%s\n' \
    "${go_bridge_status}" "${cpp_bridge_status}" "${forward_status}" |
    tee "${output_directory}/summary.txt"
  exit 1
fi
printf 'PASS RTSP media matrix: Go and C++ bridges decoded %s frames\n' \
  "${media_frames}" | tee "${output_directory}/summary.txt"
