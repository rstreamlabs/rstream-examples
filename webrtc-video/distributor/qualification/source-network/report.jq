. as $runs |
def range(values): values | if length == 0 then null else {minimum: min, maximum: max} end;
def numeric(path): [$runs[] | getpath(path) | select(type == "number")];
([$runs[].profile] | unique) as $profiles |
([$runs[].revision] | unique) as $revisions |
($profiles | first) as $profile |
($profile.sourceNetwork.capacityKbps > 0) as $capacity_conditioned |
($profile.sourceNetwork.lossPercent > 0) as $loss_conditioned |
{
  revision: ($revisions | first),
  runs: $run_count,
  profile: $profile,
  executionFailures: ([$runs[] | select(.executionStatus != 0)] | length),
  sourceNetwork: {
    qdiscPackets: range(numeric(["sourceNetwork", "qdisc", "packets"])),
    qdiscDrops: range(numeric(["sourceNetwork", "qdisc", "drops"])),
    twccFeedbackPackets: range(numeric(["sourceNetwork", "twccFeedbackPacketsDelta"])),
    adaptiveBitrateUpdates: range(numeric(["sourceNetwork", "adaptiveBitrateUpdatesDelta"])),
    retransmittedPackets: range(numeric(["sourceNetwork", "pacerSentRetransmissionDelta"])),
    fecPackets: range(numeric(["sourceNetwork", "pacerSentFECDelta"])),
    repairedRTX: range(numeric(["adapter", "repaired_rtx"])),
    repairedFEC: range(numeric(["adapter", "repaired_fec"])),
    decodedFramesPerSecond: range(numeric(["phases", "sourceNetwork", "decodedFramesPerSecond"])),
    frameDropRatio: range(numeric(["sourceNetwork", "frameDropRatio"])),
    freezeRatio: range(numeric(["sourceNetwork", "freezeRatio"]))
  },
  bitrate: {
    baselineEncoderTargetKbps: range(numeric(["phases", "baseline", "encoderTargetKbps", "medianLast10Seconds"])),
    conditionedEncoderTargetKbps: range(numeric(["phases", "sourceNetwork", "encoderTargetKbps", "medianLast10Seconds"])),
    recoveryEncoderTargetKbps: range(numeric(["phases", "recovery", "encoderTargetKbps", "medianLast10Seconds"]))
  },
  setupMilliseconds: range(numeric(["setup", "peerToFirstDecodedFrameMilliseconds"])),
  resources: {
    adapterPeakCPUCoreRatio: range(numeric(["resources", "components", "distributor", "cpuCoreRatio", "maximum"])),
    adapterPeakResidentBytes: range(numeric(["resources", "components", "distributor", "residentBytes", "maximum"])),
    producerPeakCPUCoreRatio: range(numeric(["resources", "components", "producer", "cpuCoreRatio", "maximum"])),
    producerPeakResidentBytes: range(numeric(["resources", "components", "producer", "residentBytes", "maximum"]))
  },
  gates: {
    exactRunCount: (($runs | length) == $run_count),
    allRunnersSucceeded: ([$runs[].executionStatus == 0] | all),
    allResultsPassed: ([$runs[].passed == true] | length == $run_count and all),
    sameRevision: (($revisions | length) == 1),
    sameProfile: (($profiles | length) == 1),
    customAdapterModeOnly: ([$runs[].mode == "mediamtx"] | all),
    edgeAuthenticationEnabled: ([$runs[].profile.edgeAuthentication == true] | all),
    producerToAdapterOnly: ([$runs[] |
      .profile.sourceNetwork.enabled == true and
      .sourceNetwork.scope == "producer-to-adapter" and
      .sourceNetwork.destination.port == null and
      .profile.viewerNetwork.enabled == false
    ] | all),
    causalNetworkEvidence: ([$runs[].gates.sourceNetworkCausality == true] | all),
    controlledResponse: ([$runs[].gates.sourceNetworkResponse == true] | all),
    recoveredTarget: ([$runs[].gates.sourceTargetRecovery == true] | all),
    continuousPlayback: ([$runs[].gates.playback == true] | all),
    capacityResponse: (
      if $capacity_conditioned then
        [$runs[] |
          .sourceNetwork.adaptiveBitrateUpdatesDelta > 0 and
          .phases.sourceNetwork.encoderTargetKbps.medianLast10Seconds <= (.profile.sourceNetwork.capacityKbps * 1.1) and
          .phases.recovery.encoderTargetKbps.medianLast10Seconds >= (.phases.baseline.encoderTargetKbps.medianLast10Seconds * 0.8)
        ] | all
      else true end
    ),
    repairResponse: (
      if $loss_conditioned then
        [$runs[] |
          .sourceNetwork.qdisc.drops > 0 and
          .sourceNetwork.pacerSentRetransmissionDelta > 0 and
          ((.adapter.repaired_rtx + .adapter.repaired_fec) > 0)
        ] | all
      else true end
    ),
    runtimeIntegrity: ([$runs[] |
      .gates.runtimeMediaIntegrity == true and
      .gates.adapterIntegrity == true and
      .gates.resourceLifecycle == true
    ] | all),
    performanceEnvironment: ([$runs[].gates.performanceEnvironment == true] | all),
    qualityEvidenceComplete: ([$runs[] |
      .phases.baseline.averageDecodedQP >= 0 and
      .phases.sourceNetwork.averageDecodedQP >= 0 and
      .phases.recovery.averageDecodedQP >= 0
    ] | all),
    resourceEvidenceComplete: ([$runs[] |
      (.resources.components | keys | sort) == ["browser", "distributor", "producer"] and
      ([.resources.components[] | .samples >= 2 and .residentBytes.maximum > 0] | all)
    ] | all)
  }
}
| .passed = ([.gates[]] | all)
| .publishable = (.passed and ([$runs[].workingTreeDirty] | any | not))

