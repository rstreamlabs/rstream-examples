export class PathStability {
  constructor(requiredMilliseconds = 3000, scope = "end-to-end") {
    if (!Number.isFinite(requiredMilliseconds) || requiredMilliseconds <= 0) {
      throw new Error("required stability must be positive");
    }
    if (!new Set(["end-to-end", "viewer"]).has(scope)) {
      throw new Error("path scope must be end-to-end or viewer");
    }
    this.requiredMilliseconds = requiredMilliseconds;
    this.scope = scope;
    this.key = "";
    this.since = 0;
  }

  observe(sample, observedAtMilliseconds) {
    const key = candidatePathKey(sample, this.scope);
    if (!key || sample.peerConnectionState !== "connected") {
      this.key = "";
      this.since = 0;
      return false;
    }
    if (key !== this.key) {
      this.key = key;
      this.since = observedAtMilliseconds;
      return false;
    }
    return observedAtMilliseconds - this.since >= this.requiredMilliseconds;
  }
}

export function pathMatchesPolicy(sample, policy, scope = "end-to-end") {
  const candidateTypes =
    scope === "viewer"
      ? [sample.localCandidateType, sample.remoteCandidateType]
      : [
          sample.localCandidateType,
          sample.remoteCandidateType,
          sample.producerLocalCandidateType,
          sample.producerRemoteCandidateType,
        ];
  const candidatesKnown = candidateTypes.every(Boolean);
  const allRelay = candidateTypes.every((type) => type === "relay");
  const usesRelay = candidateTypes.includes("relay");
  return policy === "relay" ? allRelay : candidatesKnown && !usesRelay;
}

export function candidatePathKey(sample, scope = "end-to-end") {
  const viewerFields = [
    sample.localCandidateType,
    sample.localCandidateAddress,
    sample.localCandidatePort,
    sample.localCandidateProtocol,
    sample.remoteCandidateType,
    sample.remoteCandidateAddress,
    sample.remoteCandidatePort,
    sample.remoteCandidateProtocol,
  ];
  const producerFields = [
    sample.producerLocalCandidateType,
    sample.producerLocalCandidateProtocol,
    sample.producerLocalCandidateURL,
    sample.producerLocalRelayProtocol,
    sample.producerRemoteCandidateType,
    sample.producerRemoteCandidateProtocol,
  ];
  const fields =
    scope === "viewer" ? viewerFields : [...viewerFields, ...producerFields];
  if (
    !sample.localCandidateType ||
    !sample.remoteCandidateType ||
    (scope !== "viewer" &&
      (!sample.producerLocalCandidateType ||
        !sample.producerRemoteCandidateType ||
        (sample.producerLocalCandidateType === "relay" &&
          (!sample.producerLocalCandidateURL ||
            !sample.producerLocalRelayProtocol ||
            !sample.remoteCandidateAddress))))
  ) {
    return "";
  }
  return fields.map((value) => String(value ?? "")).join("|");
}
