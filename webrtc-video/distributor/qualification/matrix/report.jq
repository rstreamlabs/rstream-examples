$capacity[0] as $capacity_report |
$impairment[0] as $impairment_report |
$fanout[0] as $fanout_report |
$source_capacity[0] as $source_capacity_report |
$source_impairment[0] as $source_impairment_report |
([
  $capacity_report.revision,
  $impairment_report.revision,
  $fanout_report.revision,
  $source_capacity_report.revision,
  $source_impairment_report.revision
] | unique) as $revisions |
def status_matches(report; status):
  if report.passed then status == 0 else status != 0 end;
{
  revision: ($revisions | first),
  runnerStatus: {
    capacity: $capacity_status,
    impairment: $impairment_status,
    fanout: $fanout_status,
    sourceCapacity: $source_capacity_status,
    sourceImpairment: $source_impairment_status
  },
  profiles: {
    capacity: $capacity_report.profile,
    impairment: $impairment_report.profile,
    sourceCapacity: $source_capacity_report.profile,
    sourceImpairment: $source_impairment_report.profile,
    fanoutReaders: [$fanout_report.phases[].readers]
  },
  evidence: {
    capacity: $capacity_report,
    impairment: $impairment_report,
    fanout: $fanout_report,
    sourceCapacity: $source_capacity_report,
    sourceImpairment: $source_impairment_report
  },
  gates: {
    sameRevision: (($revisions | length) == 1),
    runnerStatusMatchesReports: (
      status_matches($capacity_report; $capacity_status) and
      status_matches($impairment_report; $impairment_status) and
      status_matches($fanout_report; $fanout_status) and
      status_matches($source_capacity_report; $source_capacity_status) and
      status_matches($source_impairment_report; $source_impairment_status)
    ),
    componentReportsPassed: (
      $capacity_report.passed == true and
      $impairment_report.passed == true and
      $fanout_report.passed == true and
      $source_capacity_report.passed == true and
      $source_impairment_report.passed == true
    ),
    edgeAuthenticationEnabled: (
      $capacity_report.profile.edgeAuthentication == true and
      $impairment_report.profile.edgeAuthentication == true and
      $source_capacity_report.profile.edgeAuthentication == true and
      $source_impairment_report.profile.edgeAuthentication == true
    ),
    capacityProfileIsolated: (
      $capacity_report.profile.viewerNetwork.capacityKbps > 0 and
      $capacity_report.profile.viewerNetwork.delayMilliseconds == 0 and
      $capacity_report.profile.viewerNetwork.jitterMilliseconds == 0 and
      $capacity_report.profile.viewerNetwork.lossPercent == 0
    ),
    impairmentProfileIsolated: (
      $impairment_report.profile.viewerNetwork.capacityKbps == 0 and
      (
        $impairment_report.profile.viewerNetwork.delayMilliseconds > 0 or
        $impairment_report.profile.viewerNetwork.jitterMilliseconds > 0 or
        $impairment_report.profile.viewerNetwork.lossPercent > 0
      )
    ),
    sourceCapacityProfileIsolated: (
      $source_capacity_report.profile.sourceNetwork.enabled == true and
      $source_capacity_report.profile.sourceNetwork.capacityKbps > 0 and
      $source_capacity_report.profile.sourceNetwork.delayMilliseconds == 0 and
      $source_capacity_report.profile.sourceNetwork.jitterMilliseconds == 0 and
      $source_capacity_report.profile.sourceNetwork.lossPercent == 0 and
      $source_capacity_report.profile.viewerNetwork.enabled == false
    ),
    sourceImpairmentProfileIsolated: (
      $source_impairment_report.profile.sourceNetwork.enabled == true and
      $source_impairment_report.profile.sourceNetwork.capacityKbps == 0 and
      (
        $source_impairment_report.profile.sourceNetwork.delayMilliseconds > 0 or
        $source_impairment_report.profile.sourceNetwork.jitterMilliseconds > 0 or
        $source_impairment_report.profile.sourceNetwork.lossPercent > 0
      ) and
      $source_impairment_report.profile.viewerNetwork.enabled == false
    ),
    repeatedEvidence: (
      $capacity_report.runsPerMode >= 3 and
      $impairment_report.runsPerMode >= 3 and
      $fanout_report.runs >= 3 and
      $source_capacity_report.runs >= 3 and
      $source_impairment_report.runs >= 3
    ),
    directCapacityQualified: (
      $capacity_report.verdict.directProfileQualified == true and
      $capacity_report.verdict.directAdaptive == true and
      $capacity_report.gates.directSourceAdapted == true and
      $capacity_report.gates.directSourceRecovered == true
    ),
    directImpairmentQualified: (
      $impairment_report.verdict.directProfileQualified == true and
      $impairment_report.verdict.directAdaptive == null and
      $impairment_report.gates.directSourceRecovered == true
    ),
    mediaMTXImpairmentQualified: (
      $impairment_report.verdict.mediaMTXProfileQualified == true and
      $impairment_report.verdict.mediaMTXSingleRenditionAdaptive == null
    ),
    mediaMTXFanOutQualified: (
      $fanout_report.passed == true and
      [$fanout_report.phases[].readers] == [1, 4, 8]
    ),
    customAdapterCapacityQualified: (
      $source_capacity_report.passed == true and
      $source_capacity_report.gates.producerToAdapterOnly == true and
      $source_capacity_report.gates.capacityResponse == true and
      $source_capacity_report.gates.recoveredTarget == true
    ),
    customAdapterRepairQualified: (
      $source_impairment_report.passed == true and
      $source_impairment_report.gates.producerToAdapterOnly == true and
      $source_impairment_report.gates.repairResponse == true and
      $source_impairment_report.gates.recoveredTarget == true
    ),
    adaptiveBoundaryDemonstrated: (
      $capacity_report.verdict.mediaMTXProfileQualified == false and
      $capacity_report.verdict.mediaMTXSingleRenditionAdaptive == false and
      $capacity_report.verdict.mediaMTXSourceRespondsToViewerTWCC == false and
      $capacity_report.verdict.mediaMTXRequiresRenditionStrategy == true
    ),
    timingEvidenceComplete: ([
      $capacity_report.direct.setup.peerToFirstDecodedFrameMilliseconds.minimum,
      $capacity_report.mediamtx.setup.peerToFirstDecodedFrameMilliseconds.minimum,
      $impairment_report.direct.setup.peerToFirstDecodedFrameMilliseconds.minimum,
      $impairment_report.mediamtx.setup.peerToFirstDecodedFrameMilliseconds.minimum,
      $source_capacity_report.setupMilliseconds.minimum,
      $source_impairment_report.setupMilliseconds.minimum,
      $capacity_report.direct.teardown.whepDeleteDurationMilliseconds.maximum,
      $capacity_report.mediamtx.teardown.whepDeleteDurationMilliseconds.maximum
    ] | all(type == "number" and . >= 0)),
    qualityEvidenceComplete: ([
      $capacity_report.direct.visualQuality.viewerNetworkAverageDecodedQP.minimum,
      $capacity_report.mediamtx.visualQuality.viewerNetworkAverageDecodedQP.minimum,
      $impairment_report.direct.visualQuality.viewerNetworkAverageDecodedQP.minimum,
      $impairment_report.mediamtx.visualQuality.viewerNetworkAverageDecodedQP.minimum,
      $source_capacity_report.bitrate.conditionedEncoderTargetKbps.minimum,
      $source_impairment_report.sourceNetwork.repairedRTX.minimum
    ] | all(type == "number" and . >= 0)),
    resourceEvidenceComplete: (
      $capacity_report.gates.resourceReportsComplete == true and
      $impairment_report.gates.resourceReportsComplete == true and
      $source_capacity_report.gates.resourceEvidenceComplete == true and
      $source_impairment_report.gates.resourceEvidenceComplete == true and
      $fanout_report.process.peakResidentBytes.maximum > 0 and
      $fanout_report.process.cpuCoreRatio.maximum >= 0
    )
  },
  productVerdict: {
    direct: (
      if
        $capacity_report.verdict.directProfileQualified == true and
        $capacity_report.gates.directSourceRecovered == true and
        $impairment_report.verdict.directProfileQualified == true and
        $impairment_report.gates.directSourceRecovered == true
      then "go" else "no-go" end
    ),
    mediaMTXSingleRenditionFanOut: (
      if
        $fanout_report.passed == true and
        $impairment_report.verdict.mediaMTXProfileQualified == true
      then "go-with-viewer-capacity-admission" else "no-go" end
    ),
    mediaMTXWithRstreamAdapter: (
      if
        $source_capacity_report.passed == true and
        $source_impairment_report.passed == true and
        $fanout_report.passed == true
      then "go" else "no-go" end
    ),
    mediaMTXHeterogeneousAdaptive: "no-go",
    requiredAction: "add a qualified rendition strategy before serving heterogeneous viewer capacities"
  }
}
| .passed = ([.gates[]] | all)
| .publishable = (
    .passed and
    .evidence.capacity.publishable and
    .evidence.impairment.publishable and
    .evidence.fanout.publishable and
    .evidence.sourceCapacity.publishable and
    .evidence.sourceImpairment.publishable
  )
