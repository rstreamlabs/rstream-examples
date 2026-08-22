#!/usr/bin/env bash
set -Eeuo pipefail

mkdir -p "$1"
if ((RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS > 0)); then
  source_network='{"enabled":true,"capacityKbps":4000,"delayMilliseconds":0,"jitterMilliseconds":0,"lossPercent":0}'
  repaired_rtx=0
else
  source_network='{"enabled":true,"capacityKbps":0,"delayMilliseconds":60,"jitterMilliseconds":15,"lossPercent":1}'
  repaired_rtx=8
fi
jq -n --argjson source_network "${source_network}" --argjson repaired_rtx "${repaired_rtx}" '{
  revision: "revision",
  runs: 3,
  profile: {edgeAuthentication: true, sourceNetwork: $source_network, viewerNetwork: {enabled: false}},
  setupMilliseconds: {minimum: 500, maximum: 750},
  bitrate: {conditionedEncoderTargetKbps: {minimum: 3800, maximum: 3900}},
  sourceNetwork: {repairedRTX: {minimum: $repaired_rtx, maximum: $repaired_rtx}},
  gates: {producerToAdapterOnly: true, capacityResponse: true, repairResponse: true, recoveredTarget: true, resourceEvidenceComplete: true},
  passed: true,
  publishable: true
}' >"$1/summary.json"
