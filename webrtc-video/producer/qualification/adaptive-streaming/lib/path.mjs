export class PathStability {
  constructor(requiredMilliseconds = 3000) {
    if (!Number.isFinite(requiredMilliseconds) || requiredMilliseconds <= 0) {
      throw new Error("required stability must be positive");
    }
    this.requiredMilliseconds = requiredMilliseconds;
    this.key = "";
    this.since = 0;
  }

  observe(sample, observedAtMilliseconds) {
    const key = candidatePathKey(sample);
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

export function pathMatchesPolicy(sample, policy) {
  const candidateTypes = [
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

export function candidatePathKey(sample) {
  const fields = [
    sample.localCandidateType,
    sample.localCandidateAddress,
    sample.localCandidatePort,
    sample.localCandidateProtocol,
    sample.remoteCandidateType,
    sample.remoteCandidateAddress,
    sample.remoteCandidatePort,
    sample.remoteCandidateProtocol,
    sample.producerLocalCandidateType,
    sample.producerLocalCandidateProtocol,
    sample.producerLocalCandidateURL,
    sample.producerLocalRelayProtocol,
    sample.producerRemoteCandidateType,
    sample.producerRemoteCandidateProtocol,
  ];
  if (
    !sample.localCandidateType ||
    !sample.remoteCandidateType ||
    !sample.producerLocalCandidateType ||
    !sample.producerRemoteCandidateType ||
    (sample.producerLocalCandidateType === "relay" &&
      (!sample.producerLocalCandidateURL ||
        !sample.producerLocalRelayProtocol ||
        !sample.remoteCandidateAddress))
  ) {
    return "";
  }
  return fields.map((value) => String(value ?? "")).join("|");
}
