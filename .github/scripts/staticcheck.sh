#!/usr/bin/env bash
set -euo pipefail

# Keep staticcheck's export-data reader aligned with the repository Go version.
tools_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../tools" && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT
"${GO:-go}" -C "$tools_dir" build -o "$scratch/staticcheck" honnef.co/go/tools/cmd/staticcheck
"$scratch/staticcheck" "$@"
