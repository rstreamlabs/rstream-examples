. as $runs |
def values(field): [$runs[].process[field]];
def range(values): {minimum: (values | min), maximum: (values | max)};
{
  revision: $revision,
  workingTreeDirty: $working_tree_dirty,
  runs: ($runs | length),
  mediaMTXVersions: ([$runs[].mediaMTXVersion] | unique),
  sourceSessions: [$runs[].sourceSessions],
  sourceDeletes: [$runs[].sourceDeletes],
  sourceTWCCPackets: range([$runs[].sourceTWCCPackets]),
  readerLimits: ([$runs[].readerLimit] | unique),
  saturation: {
    rejected: ([$runs[].saturationRejected] | all),
    rejectMilliseconds: range([$runs[].saturationRejectMilliseconds]),
    existingViewerPayloadRatio: range([$runs[].saturationViewerPayloadRatio[]])
  },
  setupMilliseconds: {
    firstViewer: range([$runs[].firstViewerSetupMilliseconds]),
    warmP95: range([$runs[].warmViewerSetupP95Milliseconds]),
    churnP95: range([$runs[].churnSetupP95Milliseconds])
  },
  process: {
    peakResidentBytes: range(values("peakResidentBytes")),
    cpuCoreRatio: range(values("cpuCoreRatio")),
    peakProcesses: range(values("peakProcesses"))
  },
  phases: [1, 4, 8] | map(. as $readers | {
    readers: $readers,
    inboundBitsPerSecond: range([$runs[] | .phases[] | select(.readers == $readers) | .inboundBitsPerSecond]),
    outboundBitsPerSecond: range([$runs[] | .phases[] | select(.readers == $readers) | .outboundBitsPerSecond])
  }),
  gates: {
    allRunsPassed: ([$runs[].passed] | all),
    oneSourcePerRun: ([$runs[].sourceSessions == 1] | all),
    oneSourceDeletePerRun: ([$runs[].sourceDeletes == 1] | all),
    sourceFeedbackInEveryRun: ([$runs[].sourceTWCCPackets > 0] | all),
    boundedReaderLimit: (([$runs[].readerLimit] | unique) == [8]),
    saturatedViewerRejected: ([$runs[].saturationRejected] | all),
    saturatedViewerRejectedQuickly: ([$runs[].saturationRejectMilliseconds <= 2000] | all),
    existingViewersSurviveSaturation: ([$runs[].saturationViewerPayloadRatio[] | (. >= 0.99 and . <= 1.02)] | all),
    exactReaderPhases: ([$runs[] | ([.phases[].readers] == [1, 4, 8])] | all),
    phaseMetricsComplete: ([$runs[] | .phases[] | (.inboundBitsPerSecond > 0 and .outboundBitsPerSecond > 0)] | all),
    linearFanOut: ([$runs[] | (.phases[1].outboundBitsPerSecond > .phases[0].outboundBitsPerSecond * 3.8 and .phases[2].outboundBitsPerSecond > .phases[0].outboundBitsPerSecond * 7.6)] | all)
  }
}
| .passed = ([.gates[]] | all)
| .publishable = (.passed and (.workingTreeDirty | not))
