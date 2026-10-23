#!/usr/bin/env sh
set -eu

# Run the tool on the build host even when the application is cross-compiled.
# The helper and patch are shipped with the sample; no SDK release is needed.
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
exec env GOFLAGS= GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" \
  go run ../ice-patch/main.go -output out/ice-patch
