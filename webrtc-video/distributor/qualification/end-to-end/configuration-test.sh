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

reproducible_builds="$(grep -Fc 'docker build --provenance=false' "${script_directory}/run.sh")"
if [[ "${reproducible_builds}" != 3 ]]; then
  printf 'all three qualification images must disable non-deterministic provenance attestations\n' >&2
  exit 1
fi

fixture_directory="$(mktemp -d "${TMPDIR:-/tmp}/rstream-recording-config.XXXXXX")"
trap 'rm -rf "${fixture_directory}"' EXIT INT TERM
for scenario in invalid native authenticated context; do
  control=local
  mode=direct
  auth=false
  context=
  expected='local control requires direct or adaptive mediamtx mode, EDGE_AUTH=false and no RSTREAM_CONTEXT'
  case "${scenario}" in
    invalid) control=invalid; expected='RSTREAM_DISTRIBUTOR_CONTROL_PATH must be rstream or local' ;;
    native) mode=mediamtx-native ;;
    authenticated) auth=true ;;
    context) context=qualification ;;
  esac
  if output="$(RSTREAM_CONTEXT="${context}" RSTREAM_DISTRIBUTOR_MODE="${mode}" \
    RSTREAM_DISTRIBUTOR_CONTROL_PATH="${control}" RSTREAM_DISTRIBUTOR_EDGE_AUTH="${auth}" \
    "${script_directory}/run.sh" "${fixture_directory}/output" 2>&1)"; then
    printf 'incompatible local control configuration was accepted\n' >&2
    exit 1
  fi
  if [[ "${output}" != *"${expected}"* || -e "${fixture_directory}/output" ]]; then
    printf 'local control configuration did not fail before creating runtime artifacts\n' >&2
    exit 1
  fi
done

for scenario in invalid direct; do
  mode=mediamtx
  recording=invalid
  expected='RSTREAM_DISTRIBUTOR_RECORDING must be true or false'
  if [[ "${scenario}" == direct ]]; then
    mode=direct
    recording=true
    expected='recording qualification requires MediaMTX'
  fi
  if output="$(RSTREAM_CONTEXT=qualification RSTREAM_DISTRIBUTOR_MODE="${mode}" \
    RSTREAM_DISTRIBUTOR_RECORDING="${recording}" \
    "${script_directory}/run.sh" "${fixture_directory}/output" 2>&1)"; then
    printf 'incompatible recording configuration was accepted\n' >&2
    exit 1
  fi
  if [[ "${output}" != *"${expected}"* || -e "${fixture_directory}/output" ]]; then
    printf 'recording configuration did not fail before creating runtime artifacts\n' >&2
    exit 1
  fi
done

for scenario in invalid direct recording network; do
  mode=mediamtx
  cycles=true
  recording=false
  loss=0
  expected='startup cycles require the adaptive MediaMTX profile'
  case "${scenario}" in
    invalid) cycles=invalid; expected='RSTREAM_DISTRIBUTOR_STARTUP_CYCLES must be true or false' ;;
    direct) mode=direct ;;
    recording) recording=true ;;
    network) loss=1; expected='startup cycles do not combine with network impairment phases' ;;
  esac
  if output="$(RSTREAM_CONTEXT=qualification RSTREAM_DISTRIBUTOR_MODE="${mode}" \
    RSTREAM_DISTRIBUTOR_STARTUP_CYCLES="${cycles}" \
    RSTREAM_DISTRIBUTOR_RECORDING="${recording}" \
    RSTREAM_DISTRIBUTOR_SOURCE_LOSS_PERCENT="${loss}" \
    "${script_directory}/run.sh" "${fixture_directory}/output" 2>&1)"; then
    printf 'incompatible startup configuration was accepted\n' >&2
    exit 1
  fi
  if [[ "${output}" != *"${expected}"* || -e "${fixture_directory}/output" ]]; then
    printf 'startup configuration did not fail before creating runtime artifacts\n' >&2
    exit 1
  fi
done

printf 'MediaMTX qualification configuration tests passed\n'
