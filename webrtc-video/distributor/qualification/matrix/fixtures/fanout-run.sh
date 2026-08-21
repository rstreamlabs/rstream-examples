#!/usr/bin/env bash
set -Eeuo pipefail

mkdir -p "$1"
jq -n '{
  revision: "revision",
  runs: 3,
  phases: [{readers: 1}, {readers: 4}, {readers: 8}],
  process: {peakResidentBytes: {maximum: 50000000}, cpuCoreRatio: {maximum: 0.2}},
  passed: true,
  publishable: true
}' >"$1/summary.json"
