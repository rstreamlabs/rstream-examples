#!/usr/bin/env bash
set -Eeuo pipefail

mkdir -p "$1"
if ((RSTREAM_DISTRIBUTOR_VIEWER_CAPACITY_KBPS > 0)); then
  profile='{"edgeAuthentication":true,"viewerNetwork":{"capacityKbps":4000,"delayMilliseconds":0,"jitterMilliseconds":0,"lossPercent":0}}'
  direct_recovered=true
  direct_qualified=true
  mediamtx_qualified=false
  direct_adaptive=true
  mediamtx_adaptive=false
  source_twcc=false
  rendition_required=true
  passed=true
  status=0
else
  profile='{"edgeAuthentication":true,"viewerNetwork":{"capacityKbps":0,"delayMilliseconds":60,"jitterMilliseconds":15,"lossPercent":1}}'
  direct_recovered=false
  direct_qualified=false
  mediamtx_qualified=true
  direct_adaptive=null
  mediamtx_adaptive=null
  source_twcc=null
  rendition_required=false
  passed=false
  status=1
fi
jq -n \
  --argjson profile "${profile}" \
  --argjson direct_recovered "${direct_recovered}" \
  --argjson direct_qualified "${direct_qualified}" \
  --argjson mediamtx_qualified "${mediamtx_qualified}" \
  --argjson direct_adaptive "${direct_adaptive}" \
  --argjson mediamtx_adaptive "${mediamtx_adaptive}" \
  --argjson source_twcc "${source_twcc}" \
  --argjson rendition_required "${rendition_required}" \
  --argjson passed "${passed}" '
    def mode: {
      setup: {peerToFirstDecodedFrameMilliseconds: {minimum: 500, maximum: 750}},
      teardown: {whepDeleteDurationMilliseconds: {minimum: 100, maximum: 150}},
      visualQuality: {viewerNetworkAverageDecodedQP: {minimum: 24, maximum: 28}}
    };
    {
      revision: "revision",
      runsPerMode: 3,
      profile: $profile,
      direct: mode,
      mediamtx: mode,
      gates: {
        directSourceAdapted: true,
        directSourceRecovered: $direct_recovered,
        resourceReportsComplete: true
      },
      verdict: {
        directProfileQualified: $direct_qualified,
        directAdaptive: $direct_adaptive,
        mediaMTXProfileQualified: $mediamtx_qualified,
        mediaMTXSingleRenditionAdaptive: $mediamtx_adaptive,
        mediaMTXSourceRespondsToViewerTWCC: $source_twcc,
        mediaMTXRequiresRenditionStrategy: $rendition_required
      },
      passed: $passed,
      publishable: $passed
    }
  ' >"$1/summary.json"
exit "${status}"
