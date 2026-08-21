#!/usr/bin/env bash

select_distribution_mode() {
  local mode=${1:-}
  uses_mediamtx=false
  uses_adapter=false
  export uses_mediamtx uses_adapter
  case "${mode}" in
  direct)
    ;;
  mediamtx)
    uses_mediamtx=true
    uses_adapter=true
    ;;
  mediamtx-native)
    uses_mediamtx=true
    ;;
  *)
    return 1
    ;;
  esac
}
