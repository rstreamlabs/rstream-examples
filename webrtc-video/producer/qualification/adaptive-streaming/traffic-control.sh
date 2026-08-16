#!/bin/sh

set -eu

tc_command="${RSTREAM_TC_COMMAND:-tc}"
sleep_command="${RSTREAM_SLEEP_COMMAND:-sleep}"
evidence_directory=""
network_interface=""
address_family=""
destination_address=""
destination_port=""
transport_protocol=""
match_destination_port=""
queue_limit_packets=""
capacity_step_one_kbps=""
capacity_step_two_kbps=""
capacity_step_three_kbps=""
capacity_kbps=""
transition_step_seconds=""
constrained_steady_seconds=""
impaired_seconds=""
recovery_capacity_kbps=""
recovery_seconds=""
shaping_initialized=0

usage() {
  printf '%s\n' \
    'usage: traffic-control.sh --evidence-directory PATH --address-family 4|6' \
    '  --network-interface INTERFACE' \
    '  --destination-address ADDRESS --destination-port PORT' \
    '  --transport-protocol udp|tcp --match-destination-port true|false' \
    '  --queue-limit-packets COUNT --capacity-step-one-kbps KBPS' \
    '  --capacity-step-two-kbps KBPS --capacity-step-three-kbps KBPS' \
    '  --capacity-kbps KBPS --transition-step-seconds SECONDS' \
    '  --constrained-steady-seconds SECONDS --impaired-seconds SECONDS' \
    '  --recovery-capacity-kbps KBPS --recovery-seconds SECONDS' >&2
}

require_value() {
  if [ "$#" -lt 2 ] || [ -z "$2" ]; then
    printf 'missing value for %s\n' "$1" >&2
    usage
    exit 2
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
  --evidence-directory)
    require_value "$@"
    evidence_directory="$2"
    shift 2
    ;;
  --network-interface)
    require_value "$@"
    network_interface="$2"
    shift 2
    ;;
  --address-family)
    require_value "$@"
    address_family="$2"
    shift 2
    ;;
  --destination-address)
    require_value "$@"
    destination_address="$2"
    shift 2
    ;;
  --destination-port)
    require_value "$@"
    destination_port="$2"
    shift 2
    ;;
  --transport-protocol)
    require_value "$@"
    transport_protocol="$2"
    shift 2
    ;;
  --match-destination-port)
    require_value "$@"
    match_destination_port="$2"
    shift 2
    ;;
  --queue-limit-packets)
    require_value "$@"
    queue_limit_packets="$2"
    shift 2
    ;;
  --capacity-step-one-kbps)
    require_value "$@"
    capacity_step_one_kbps="$2"
    shift 2
    ;;
  --capacity-step-two-kbps)
    require_value "$@"
    capacity_step_two_kbps="$2"
    shift 2
    ;;
  --capacity-step-three-kbps)
    require_value "$@"
    capacity_step_three_kbps="$2"
    shift 2
    ;;
  --capacity-kbps)
    require_value "$@"
    capacity_kbps="$2"
    shift 2
    ;;
  --transition-step-seconds)
    require_value "$@"
    transition_step_seconds="$2"
    shift 2
    ;;
  --constrained-steady-seconds)
    require_value "$@"
    constrained_steady_seconds="$2"
    shift 2
    ;;
  --impaired-seconds)
    require_value "$@"
    impaired_seconds="$2"
    shift 2
    ;;
  --recovery-capacity-kbps)
    require_value "$@"
    recovery_capacity_kbps="$2"
    shift 2
    ;;
  --recovery-seconds)
    require_value "$@"
    recovery_seconds="$2"
    shift 2
    ;;
  *)
    printf 'unknown argument: %s\n' "$1" >&2
    usage
    exit 2
    ;;
  esac
done

positive_integer() {
  case "$1" in
  '' | *[!0-9]* | 0)
    return 1
    ;;
  esac
}

if [ -z "${evidence_directory}" ] || [ -z "${destination_address}" ] || \
  [ -z "${network_interface}" ]; then
  printf 'evidence directory, network interface, and destination address are required\n' >&2
  usage
  exit 2
fi
case "${network_interface}" in
*[!a-zA-Z0-9_.:@-]*)
  printf 'network interface contains unsupported characters\n' >&2
  exit 2
  ;;
esac
case "${address_family}" in
4 | 6)
  ;;
*)
  printf 'address family must be 4 or 6\n' >&2
  exit 2
  ;;
esac
case "${transport_protocol}" in
udp)
  protocol_number=17
  ;;
tcp)
  protocol_number=6
  ;;
*)
  printf 'transport protocol must be udp or tcp\n' >&2
  exit 2
  ;;
esac
case "${match_destination_port}" in
true | false)
  ;;
*)
  printf 'match-destination-port must be true or false\n' >&2
  exit 2
  ;;
esac
for value in \
  "${destination_port}" \
  "${queue_limit_packets}" \
  "${capacity_step_one_kbps}" \
  "${capacity_step_two_kbps}" \
  "${capacity_step_three_kbps}" \
  "${capacity_kbps}" \
  "${transition_step_seconds}" \
  "${constrained_steady_seconds}" \
  "${impaired_seconds}" \
  "${recovery_capacity_kbps}" \
  "${recovery_seconds}"; do
  if ! positive_integer "${value}"; then
    printf 'numeric traffic-control arguments must be positive integers\n' >&2
    exit 2
  fi
done
if [ "${destination_port}" -gt 65535 ]; then
  printf 'destination port must be at most 65535\n' >&2
  exit 2
fi

clear_shaping() {
  if [ "${shaping_initialized}" -eq 1 ]; then
    "${tc_command}" qdisc del dev "${network_interface}" root >/dev/null 2>&1 || true
    shaping_initialized=0
  fi
}

interrupted() {
  signal_status="$1"
  exit "${signal_status}"
}

finish() {
  status=$?
  trap - EXIT
  clear_shaping
  exit "${status}"
}

trap finish EXIT
trap 'interrupted 130' INT
trap 'interrupted 143' TERM
trap 'interrupted 129' HUP

apply_shaping() {
  rate_kbps="$1"
  delay="$2"
  jitter="$3"
  loss="$4"
  action=change
  initializing=0
  if [ "${shaping_initialized}" -eq 0 ]; then
    "${tc_command}" qdisc add dev "${network_interface}" root handle 1: prio bands 2 \
      priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
    shaping_initialized=1
    initializing=1
    action=add
  fi
  set -- delay "${delay}"
  if [ "${jitter}" != 0ms ]; then
    set -- "$@" "${jitter}" distribution normal
  fi
  "${tc_command}" qdisc "${action}" dev "${network_interface}" parent 1:2 handle 20: netem \
    limit "${queue_limit_packets}" rate "${rate_kbps}kbit" \
    "$@" loss random "${loss}"
  if [ "${initializing}" -eq 0 ]; then
    return
  fi
  if [ "${address_family}" = 4 ]; then
    if [ "${match_destination_port}" = true ]; then
      "${tc_command}" filter add dev "${network_interface}" protocol ip parent 1: prio 1 u32 \
        match ip protocol "${protocol_number}" 0xff \
        match ip dst "${destination_address}/32" \
        match ip dport "${destination_port}" 0xffff flowid 1:2
    else
      "${tc_command}" filter add dev "${network_interface}" protocol ip parent 1: prio 1 u32 \
        match ip protocol "${protocol_number}" 0xff \
        match ip dst "${destination_address}/32" flowid 1:2
    fi
  elif [ "${match_destination_port}" = true ]; then
    "${tc_command}" filter add dev "${network_interface}" protocol ipv6 parent 1: prio 1 u32 \
      match ip6 dst "${destination_address}/128" \
      match ip6 protocol "${protocol_number}" 0xff \
      match ip6 dport "${destination_port}" 0xffff flowid 1:2
  else
    "${tc_command}" filter add dev "${network_interface}" protocol ipv6 parent 1: prio 1 u32 \
      match ip6 dst "${destination_address}/128" \
      match ip6 protocol "${protocol_number}" 0xff flowid 1:2
  fi
}

capture() {
  name="$1"
  qdisc_temporary="${evidence_directory}/qdisc-${name}.json.tmp"
  filter_temporary="${evidence_directory}/filter-${name}.json.tmp"
  "${tc_command}" -s -j qdisc show dev "${network_interface}" >"${qdisc_temporary}"
  "${tc_command}" -s -j filter show dev "${network_interface}" parent 1: >"${filter_temporary}"
  mv "${qdisc_temporary}" "${evidence_directory}/qdisc-${name}.json"
  mv "${filter_temporary}" "${evidence_directory}/filter-${name}.json"
}

require_shaped_traffic() {
  name="$1"
  packets="$(jq '[.[] | select(.kind == "netem") | (.packets // 0)] | add // 0' \
    "${evidence_directory}/qdisc-${name}.json")"
  case "${packets}" in
  '' | *[!0-9]*)
    printf 'invalid shaped packet count after %s: %s\n' "${name}" "${packets}" >&2
    exit 3
    ;;
  esac
  if [ "${packets}" -eq 0 ]; then
    printf 'selective traffic-control target carried no packets after %s\n' \
      "${name}" >&2
    exit 3
  fi
}

emit() {
  printf '%s\n' "$1"
}

mkdir -p "${evidence_directory}"
apply_shaping "${capacity_step_one_kbps}" 40ms 10ms 0%
capture constrained-step-1-start
emit constrained-started
"${sleep_command}" "${transition_step_seconds}"
capture constrained-step-1
require_shaped_traffic constrained-step-1
apply_shaping "${capacity_step_two_kbps}" 50ms 10ms 0%
capture constrained-step-2-start
"${sleep_command}" "${transition_step_seconds}"
capture constrained-step-2
apply_shaping "${capacity_step_three_kbps}" 65ms 15ms 0%
capture constrained-step-3-start
"${sleep_command}" "${transition_step_seconds}"
capture constrained-step-3
apply_shaping "${capacity_kbps}" 80ms 20ms 0%
capture constrained-steady-start
"${sleep_command}" "${constrained_steady_seconds}"
capture constrained-steady
apply_shaping "${capacity_kbps}" 120ms 30ms 2%
capture impaired-start
emit impaired-started
"${sleep_command}" "${impaired_seconds}"
capture impaired
apply_shaping "${recovery_capacity_kbps}" 0ms 0ms 0%
capture recovery-start
emit recovery-started
"${sleep_command}" "${recovery_seconds}"
capture recovery
require_shaped_traffic recovery
