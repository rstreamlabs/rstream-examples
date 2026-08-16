#!/usr/bin/env bash

set -Eeuo pipefail

qualification_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
example_directory="$(cd "${qualification_directory}/.." && pwd -P)"
repository_directory="$(git -C "${example_directory}" rev-parse --show-toplevel)"
context_name="${RSTREAM_CONTEXT:-}"
# Used by every scenario after sourcing this file.
# shellcheck disable=SC2034
context_environment=()
if [[ -n "${context_name}" ]]; then
  # shellcheck disable=SC2034
  context_environment=(env RSTREAM_CONTEXT="${context_name}")
fi
media_frames="${RSTREAM_MEDIA_FRAMES:-300}"
media_width="${RSTREAM_MEDIA_WIDTH:-320}"
media_height="${RSTREAM_MEDIA_HEIGHT:-240}"
frame_bytes=$((media_width * media_height * 3 / 2))
expected_bytes=$((media_frames * frame_bytes))
current_pids=()

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'required command not found: %s\n' "$1" >&2
    return 1
  fi
}

resolve_binary() {
  local configured="$1"
  local fallback="$2"
  if [[ -n "${configured}" ]]; then
    if [[ ! -x "${configured}" ]]; then
      printf 'configured binary is not executable: %s\n' "${configured}" >&2
      return 1
    fi
    printf '%s\n' "${configured}"
    return
	fi
	local resolved
	resolved="$(command -v "${fallback}" 2>/dev/null || true)"
	if [[ -z "${resolved}" ]]; then
		printf 'required command not found: %s; configure its explicit path instead\n' \
			"${fallback}" >&2
		return 1
	fi
	printf '%s\n' "${resolved}"
}

resolve_timeout() {
  if command -v timeout >/dev/null 2>&1; then
    command -v timeout
    return
  fi
	if command -v gtimeout >/dev/null 2>&1; then
		command -v gtimeout
		return
	fi
	printf 'required command not found: timeout or gtimeout\n' >&2
	return 1
}

shell_quote() {
  printf '%q' "$1"
}

validate_positive_integer() {
  local name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s must be a positive integer, got %s\n' "${name}" "${value}" >&2
    return 1
  fi
}

prepare_output_directory() {
  local requested="$1"
  if [[ -e "${requested}" ]] &&
    [[ -n "$(find "${requested}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
    printf 'output directory is not empty: %s\n' "${requested}" >&2
    return 1
  fi
  mkdir -p "${requested}"
  (cd "${requested}" && pwd -P)
}

register_pid() {
  current_pids+=("$1")
}

forget_pid() {
  local forgotten="$1"
  local remaining=()
  local pid
  for pid in "${current_pids[@]}"; do
    if [[ "${pid}" != "${forgotten}" ]]; then
      remaining+=("${pid}")
    fi
  done
  current_pids=("${remaining[@]}")
}

stop_pid() {
  local pid="$1"
  local signal="${2:-INT}"
  local deadline=$((SECONDS + 8))
  local status=0
  if ! kill -0 "${pid}" 2>/dev/null; then
    wait "${pid}" 2>/dev/null || status=$?
    forget_pid "${pid}"
    return "${status}"
  fi
  kill -"${signal}" "${pid}" 2>/dev/null || true
  while kill -0 "${pid}" 2>/dev/null && ((SECONDS < deadline)); do
    sleep 0.1
  done
  if kill -0 "${pid}" 2>/dev/null; then
    kill -TERM "${pid}" 2>/dev/null || true
    sleep 1
  fi
  if kill -0 "${pid}" 2>/dev/null; then
    kill -KILL "${pid}" 2>/dev/null || true
  fi
  wait "${pid}" 2>/dev/null || status=$?
  forget_pid "${pid}"
  return "${status}"
}

cleanup_processes() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  local pid
  for pid in "${current_pids[@]}"; do
    stop_pid "${pid}" TERM >/dev/null 2>&1
  done
  exit "${status}"
}

wait_until_listening() {
  local host="$1"
  local port="$2"
  local deadline=$((SECONDS + 10))
  while ((SECONDS < deadline)); do
    if nc -z "${host}" "${port}" >/dev/null 2>&1; then
      return
    fi
    sleep 0.2
  done
  return 1
}

wait_until_file_size() {
  local path="$1"
  local minimum_size="$2"
  local timeout_seconds="$3"
  local deadline=$((SECONDS + timeout_seconds))
  while ((SECONDS < deadline)); do
    local actual_size=0
    if [[ -f "${path}" ]]; then
      actual_size="$(wc -c <"${path}" | tr -d ' ')"
    fi
    if ((actual_size >= minimum_size)); then
      return
    fi
    sleep 0.2
  done
  return 1
}

assert_exact_frames() {
  local label="$1"
  local path="$2"
  local result_path="$3"
  local actual_bytes
  actual_bytes="$(wc -c <"${path}" | tr -d ' ')"
  if ((actual_bytes != expected_bytes)); then
    printf 'FAIL %s decoded_bytes=%s expected=%s decoded_frames=%s expected_frames=%s\n' \
      "${label}" "${actual_bytes}" "${expected_bytes}" \
      "$((actual_bytes / frame_bytes))" "${media_frames}" | tee "${result_path}"
    return 1
  fi
  local digest
  digest="$(shasum -a 256 "${path}" | awk '{print $1}')"
  printf 'PASS %s decoded_frames=%s decoded_bytes=%s sha256=%s\n' \
    "${label}" "${media_frames}" "${actual_bytes}" "${digest}" | tee "${result_path}"
}

write_manifest() {
  local output_directory="$1"
  local scenario="$2"
  local go_binary="${3:-}"
  local cpp_binary="${4:-}"
  local revision
  revision="$(git -C "${repository_directory}" rev-parse HEAD)"
  local dirty=false
  if [[ -n "$(git -C "${repository_directory}" status --porcelain=v1)" ]]; then
    dirty=true
  fi
  python3 "${qualification_directory}/write_manifest.py" \
    "${output_directory}/manifest.json" \
    "${scenario}" \
    "${revision}" \
    "${dirty}" \
    "${go_binary}" \
    "${cpp_binary}" \
    "${media_frames}" \
    "${media_width}" \
    "${media_height}"
}

trap cleanup_processes EXIT INT TERM
