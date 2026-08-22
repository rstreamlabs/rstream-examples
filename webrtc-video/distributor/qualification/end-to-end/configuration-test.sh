#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
distributor_directory="$(cd "${script_directory}/../.." && pwd -P)"
expected_track_gather_timeout=250ms

configured_track_gather_timeout="$({
  awk '$1 == "webrtcTrackGatherTimeout:" {print $2}' \
    "${distributor_directory}/mediamtx.yml"
} | tail -n 1)"
if [[ "${configured_track_gather_timeout}" != "${expected_track_gather_timeout}" ]]; then
  printf 'MediaMTX track-gather timeout is %s, want %s\n' \
    "${configured_track_gather_timeout:-unset}" \
    "${expected_track_gather_timeout}" >&2
  exit 1
fi

if ! grep -Fq \
  "webrtcTrackGatherTimeout: \"${expected_track_gather_timeout}\"" \
  "${script_directory}/run.sh"; then
  printf 'native MediaMTX qualification config must use track-gather timeout %s\n' \
    "${expected_track_gather_timeout}" >&2
  exit 1
fi
if ! grep -Fq \
  "whepTrackGatherTimeout: \"${expected_track_gather_timeout}\"" \
  "${script_directory}/run.sh"; then
  printf 'native MediaMTX WHEP source must use track-gather timeout %s\n' \
    "${expected_track_gather_timeout}" >&2
  exit 1
fi

printf 'MediaMTX qualification configuration tests passed\n'
