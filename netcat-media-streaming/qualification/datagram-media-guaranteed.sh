#!/usr/bin/env bash

set -Eeuo pipefail

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
export RSTREAM_DATAGRAM_GUARANTEED_DELIVERY=true
exec "${script_directory}/datagram-media.sh" "$@"
