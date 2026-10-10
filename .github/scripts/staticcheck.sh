#!/usr/bin/env bash

set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
output=$(mktemp -d)
trap 'rm -rf -- "$output"' EXIT
# Keep analysis tooling dependencies separate from the module being checked.
go -C "$root/.github/tools" build -mod=readonly -o "$output/staticcheck" honnef.co/go/tools/cmd/staticcheck
"$output/staticcheck" "$@"
