#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_directory}/common.sh"

resolve_binary "${RSTREAM_GO_BIN:-}" rstream >/dev/null
resolve_binary "${RSTREAM_CPP_BIN:-}" rstream-ncat >/dev/null
resolve_timeout >/dev/null
for command in awk ffmpeg grep gst-inspect-1.0 gst-launch-1.0 jq mediamtx nc \
	python3 shasum; do
	require_command "${command}"
done
gst-inspect-1.0 x264enc mpegtsmux rtph264pay rtpstreampay \
	rtpstreamdepay rtpjitterbuffer rtpbin rtprtxqueue avdec_h264 >/dev/null
PYTHONPATH="${script_directory}" python3 -m unittest \
	"${script_directory}/test_analyze_frames.py" \
	"${script_directory}/test_analyze_logs.py" \
	"${script_directory}/test_analyze_rtp.py" \
	"${script_directory}/test_compare_frames.py" \
	"${script_directory}/test_render_report.py" \
	"${script_directory}/test_rtp_probe.py" \
	"${script_directory}/test_write_manifest.py" >/dev/null
printf 'netcat media qualification dependencies are available\n'
