. as $records |
def results: [$records[].result | select(type == "object")];
def values(path): [results[] | getpath(path) | select(type == "number")];
def range(path): values(path) | if length == 0 then null else {minimum: min, maximum: max} end;
(results) as $results |
([$results[].revision] | unique) as $revisions |
([$results[].images.producer] | unique) as $producer_images |
([$results[].images.distributor] | unique) as $distributor_images |
([$results[].images.browser] | unique) as $browser_images |
{
  revision: ($revisions | first),
  workingTreeDirty: $working_tree_dirty,
  requestedRuns: $requested_runs,
  completedRuns: ($results | length),
  runnerStatuses: [$records[].status],
  images: {
    producer: $producer_images,
    mediaMTX: $distributor_images,
    browser: $browser_images
  },
  media: {
    framesDecoded: range(["framesDecoded"]),
    bytesReceived: range(["bytesReceived"]),
    producerTWCCFeedbackPackets: range(["producerTWCCFeedbackPackets"]),
    framesDropped: range(["framesDropped"]),
    freezes: range(["freezeCount"]),
    freezeDurationSeconds: range(["totalFreezesDurationSeconds"])
  },
  setupMilliseconds: {
    sourceToFirstFrame: range(["setup", "peerToFirstDecodedFrameMilliseconds"]),
    viewerWHEPPost: range(["setup", "whepPostDurationMilliseconds"]),
    peerConnection: range(["setup", "postToConnectedMilliseconds"])
  },
  teardownMilliseconds: range(["teardown", "whepDeleteDurationMilliseconds"]),
  resources: {
    mediaMTXPeakCPUCoreRatio: range(["resources", "components", "distributor", "cpuCoreRatio", "maximum"]),
    mediaMTXPeakResidentBytes: range(["resources", "components", "distributor", "residentBytes", "maximum"]),
    producerPeakCPUCoreRatio: range(["resources", "components", "producer", "cpuCoreRatio", "maximum"]),
    producerPeakResidentBytes: range(["resources", "components", "producer", "residentBytes", "maximum"])
  },
  gates: {
    exactRunCount: (($records | length) == $requested_runs and ($results | length) == $requested_runs),
    allRunnersSucceeded: ([$records[].status == 0] | all),
    allResultsPassed: ([$results[].passed == true] | length == $requested_runs and all),
    sameRevision: (($revisions | length) == 1 and ($revisions | first) == $revision),
    sameImages: (($producer_images | length) == 1 and ($distributor_images | length) == 1 and ($browser_images | length) == 1),
    nativeModeOnly: ([$results[].mode == "mediamtx-native"] | length == $requested_runs and all),
    edgeAuthenticationEnabled: ([$results[].profile.edgeAuthentication == true] | length == $requested_runs and all),
    exactSourceLifecycle: ([$results[] | .nativeSourceProfile == {
      required: true,
      activeSessions: 1,
      createdSessions: 1,
      negotiated: {twcc: 1, nack: 1, rtx: 0, flexfec: 0},
      fixedSourcePacing: {adaptiveUpdates: 0, adaptiveFailures: 0, queueDrops: 0, mediaFrameDrops: 0},
      activeAfterTeardown: 0
    }] | length == $requested_runs and all),
    mediaDeliveredWithoutLoss: ([$results[] | .framesDecoded >= 150 and .bytesReceived > 0 and .framesDropped == 0 and .freezeCount == 0 and .totalFreezesDurationSeconds == 0] | length == $requested_runs and all),
    feedbackAndNegotiationQualified: ([$results[] | .producerTWCCFeedbackPackets > 0 and .producerPacerSentFEC == 0 and .peerConnection.twccNegotiated and .peerConnection.nackNegotiated and (.peerConnection.rtxNegotiated | not) and (.peerConnection.flexFECNegotiated | not)] | length == $requested_runs and all),
    resultGatesComplete: ([$results[] | ([.gates[]] | all)] | length == $requested_runs and all),
    resourceEvidenceComplete: ([$results[] | (.resources.components | keys | sort) == ["browser", "distributor", "producer"] and ([.resources.components[] | .samples >= 2 and .cpuCoreRatio.maximum >= 0 and .residentBytes.maximum > 0 and .tasks.maximum > 0] | all)] | length == $requested_runs and all)
  }
}
| .passed = ([.gates[]] | all)
| .publishable = (.passed and (.workingTreeDirty | not))
