. as $runs |
def range(values): {minimum: (values | min), maximum: (values | max)};
def mode_runs(mode): [$runs[] | select(.mode == mode)];
def mode_summary(mode):
  mode_runs(mode) as $selected |
  {
    runs: ($selected | length),
    passedRuns: ([$selected[] | select(.passed)] | length),
    executionFailures: ([$selected[] | select(.executionStatus != 0)] | length),
    setupMilliseconds: range([$selected[].setupMilliseconds]),
    setup: {
      whepPostDurationMilliseconds: range([$selected[].setup.whepPostDurationMilliseconds]),
      postToConnectedMilliseconds: range([$selected[].setup.postToConnectedMilliseconds]),
      peerToFirstDecodedFrameMilliseconds: range([$selected[].setup.peerToFirstDecodedFrameMilliseconds])
    },
    teardown: {
      whepDeleteDurationMilliseconds: range([$selected[].teardown.whepDeleteDurationMilliseconds])
    },
    viewerNetwork: {
      receivedBitrateKbps: range([$selected[].phases.viewerNetwork.receivedBitrateKbps]),
      decodedFramesPerSecond: range([$selected[].phases.viewerNetwork.decodedFramesPerSecond]),
      averageDecodedQP: range([$selected[].phases.viewerNetwork.averageDecodedQP]),
      nacks: range([$selected[].phases.viewerNetwork.nacks]),
      packetsLostNetChange: range([$selected[].phases.viewerNetwork.packetsLostNetChange]),
      freezeDurationSeconds: range([$selected[].phases.viewerNetwork.freezeDurationSeconds])
    },
    sourceAdaptation: {
      baselineEncoderTargetKbps: range([$selected[].phases.baseline.encoderTargetKbps.last]),
      impairedEncoderTargetKbps: range([$selected[].phases.viewerNetwork.encoderTargetKbps.last]),
      recoveryEncoderTargetKbps: range([$selected[].phases.recovery.encoderTargetKbps.last]),
      sustainedRecoveryEncoderTargetKbps: range([$selected[].phases.recovery.encoderTargetKbps.sustainedMinimumLast10Seconds]),
      medianRecoveryEncoderTargetKbps: range([$selected[].phases.recovery.encoderTargetKbps.medianLast10Seconds]),
      adaptedRuns: ([$selected[] | select(.phases.viewerNetwork.encoderTargetKbps.last <= (.phases.baseline.encoderTargetKbps.last * 0.8))] | length),
      recoveredRuns: ([$selected[] | select(.phases.recovery.encoderTargetKbps.medianLast10Seconds >= (.phases.baseline.encoderTargetKbps.last * 0.8))] | length)
    },
    visualQuality: {
      baselineAverageDecodedQP: range([$selected[].phases.baseline.averageDecodedQP]),
      viewerNetworkAverageDecodedQP: range([$selected[].phases.viewerNetwork.averageDecodedQP]),
      recoveryAverageDecodedQP: range([$selected[].phases.recovery.averageDecodedQP])
    },
    resources: {
      producer: {
        averageCpuCoreRatio: range([$selected[].resources.components.producer.cpuCoreRatio.average]),
        p95CpuCoreRatio: range([$selected[].resources.components.producer.cpuCoreRatio.p95]),
        peakCpuCoreRatio: range([$selected[].resources.components.producer.cpuCoreRatio.maximum]),
        p95ResidentBytes: range([$selected[].resources.components.producer.residentBytes.p95]),
        peakResidentBytes: range([$selected[].resources.components.producer.residentBytes.maximum])
      },
      browser: {
        averageCpuCoreRatio: range([$selected[].resources.components.browser.cpuCoreRatio.average]),
        p95CpuCoreRatio: range([$selected[].resources.components.browser.cpuCoreRatio.p95]),
        peakCpuCoreRatio: range([$selected[].resources.components.browser.cpuCoreRatio.maximum]),
        p95ResidentBytes: range([$selected[].resources.components.browser.residentBytes.p95]),
        peakResidentBytes: range([$selected[].resources.components.browser.residentBytes.maximum])
      },
      distributor: (
        if mode == "mediamtx" then {
          averageCpuCoreRatio: range([$selected[].resources.components.distributor.cpuCoreRatio.average]),
          p95CpuCoreRatio: range([$selected[].resources.components.distributor.cpuCoreRatio.p95]),
          peakCpuCoreRatio: range([$selected[].resources.components.distributor.cpuCoreRatio.maximum]),
          p95ResidentBytes: range([$selected[].resources.components.distributor.residentBytes.p95]),
          peakResidentBytes: range([$selected[].resources.components.distributor.residentBytes.maximum]),
          peakTasks: range([$selected[].resources.components.distributor.tasks.maximum])
        } else null end
      )
    }
  };
(mode_runs("direct")) as $direct |
(mode_runs("mediamtx")) as $distributed |
([$runs[].profile] | unique | first) as $profile |
($profile.viewerNetwork.capacityKbps > 0) as $adaptation_required |
(
  $profile.viewerNetwork.capacityKbps > 0 or
  $profile.viewerNetwork.delayMilliseconds > 0 or
  $profile.viewerNetwork.jitterMilliseconds > 0 or
  $profile.viewerNetwork.lossPercent > 0
) as $network_conditioned |
{
  revision: ([$runs[].revision] | unique | first),
  profile: $profile,
  runsPerMode: $run_count,
  direct: mode_summary("direct"),
  mediamtx: mode_summary("mediamtx"),
  gates: {
    evidenceComplete: (($direct | length) == $run_count and ($distributed | length) == $run_count),
    sameRevision: (([$runs[].revision] | unique | length) == 1),
    sameProfile: (([$runs[].profile] | unique | length) == 1),
    resourceReportsComplete: ([$runs[] | (.resources.components.producer.samples >= 2 and .resources.components.browser.samples >= 2 and (if .mode == "mediamtx" then .resources.components.distributor.samples >= 2 else true end))] | all),
    directReferenceQualified: ([$direct[].passed] | all),
    directSourceAdapted: (if $adaptation_required then [$direct[] | .phases.viewerNetwork.encoderTargetKbps.last <= (.phases.baseline.encoderTargetKbps.last * 0.8)] | all else true end),
    directSourceRecovered: (if $network_conditioned then [$direct[] | .phases.recovery.encoderTargetKbps.medianLast10Seconds >= (.phases.baseline.encoderTargetKbps.last * 0.8)] | all else true end)
  },
  verdict: {
    directProfileQualified: ([$direct[].passed] | all),
    directAdaptive: (if $adaptation_required then [$direct[].passed] | all else null end),
    mediaMTXProfileQualified: ([$distributed[].passed] | all),
    mediaMTXSingleRenditionAdaptive: (if $adaptation_required then [$distributed[].passed] | all else null end),
    mediaMTXSourceRespondsToViewerTWCC: (if $adaptation_required then [$distributed[] | .phases.viewerNetwork.encoderTargetKbps.last <= (.phases.baseline.encoderTargetKbps.last * 0.8)] | all else null end),
    mediaMTXRequiresRenditionStrategy: (if $adaptation_required then (([$distributed[].passed] | all) | not) else false end)
  }
}
| .passed = ([.gates[]] | all)
| .publishable = (.passed and ([$runs[].workingTreeDirty] | any | not))
