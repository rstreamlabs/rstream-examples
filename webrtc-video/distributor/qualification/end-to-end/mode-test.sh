#!/usr/bin/env bash
set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
uses_mediamtx=false
uses_adapter=false
# shellcheck source=mode.sh
# shellcheck disable=SC1091
source "${script_directory}/mode.sh"

select_distribution_mode direct
[[ "${uses_mediamtx}" == false && "${uses_adapter}" == false ]]
select_distribution_mode mediamtx
[[ "${uses_mediamtx}" == true && "${uses_adapter}" == true ]]
select_distribution_mode mediamtx-native
[[ "${uses_mediamtx}" == true && "${uses_adapter}" == false ]]
if select_distribution_mode unsupported; then
  printf 'unsupported distribution mode was accepted\n' >&2
  exit 1
fi
