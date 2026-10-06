// Qualification-only: Playwright serializes this function into the browser.
// Native counters are updated on rendering, not on our one-second polling tick.
// Two bounded snapshots must prove that counters are constant across the cutoff.
export function installTransitionBoundary() {
  window.__rstreamTransitionBoundary?.stop();
  const phases = new Set(["viewer-network", "source-network", "recovery"]);
  const graceMilliseconds = 4000;
  const bracketMilliseconds = 250;
  const counters = [
    "freezeCount",
    "totalFreezesDurationSeconds",
    "framesDropped",
  ];
  let current = null;
  let stopped = false;
  let pending = false;
  const timers = new Set();
  const clearTimers = () => {
    for (const timer of timers) clearTimeout(timer);
    timers.clear();
  };
  const schedule = (at, callback) => {
    const timer = setTimeout(
      () => {
        timers.delete(timer);
        callback();
      },
      Math.max(0, at - performance.now()),
    );
    timers.add(timer);
  };
  const activePeer = () =>
    [...(window.__rstreamQualificationPeers || [])]
      .reverse()
      .find((peer) => peer.connectionState !== "closed");
  const fail = (state, reason) => {
    if (stopped || !state || current !== state || state.status !== "pending")
      return;
    state.status = "invalid";
    state.reason = reason;
    clearTimers();
  };
  const capture = async (state, side) => {
    if (stopped || current !== state || state.status !== "pending") return;
    if (pending) return fail(state, "stats-request-overlap");
    if (activePeer() !== state.peer) return fail(state, "peer-changed");
    pending = true;
    const requestedAtMilliseconds = performance.now();
    try {
      const reports = await state.peer.getStats();
      if (stopped || current !== state || state.status !== "pending") return;
      if (activePeer() !== state.peer) return fail(state, "peer-changed");
      const completedAtMilliseconds = performance.now();
      const inbound = [...reports.values()].filter(
        (report) =>
          report.type === "inbound-rtp" &&
          (report.kind === "video" || report.mediaType === "video") &&
          report.framesDecoded > 0,
      );
      if (inbound.length !== 1) return fail(state, "ambiguous-video-stats");
      const report = inbound[0];
      // RTCStats.timestamp uses timeOrigin + now at information collection:
      // https://www.w3.org/TR/webrtc-stats/#basic-concepts
      const sample = {
        requestedAtMilliseconds,
        completedAtMilliseconds,
        collectedAtMilliseconds: report.timestamp - performance.timeOrigin,
        id: report.id,
        ssrc: report.ssrc,
        framesDecoded: report.framesDecoded,
        framesDropped: report.framesDropped,
        freezeCount: report.freezeCount,
        totalFreezesDurationSeconds: report.totalFreezesDuration,
      };
      state[side] = sample;
      if (
        typeof sample.id !== "string" ||
        sample.id.length === 0 ||
        !Number.isSafeInteger(sample.ssrc) ||
        sample.ssrc < 0 ||
        ![sample.framesDecoded, sample.framesDropped, sample.freezeCount].every(
          (value) => Number.isSafeInteger(value) && value >= 0,
        ) ||
        !Number.isFinite(sample.totalFreezesDurationSeconds) ||
        sample.totalFreezesDurationSeconds < 0
      )
        return fail(state, "invalid-native-counters");
      const timestamp = sample.collectedAtMilliseconds;
      const cutoff = state.cutoffMilliseconds;
      if (
        !Number.isFinite(timestamp) ||
        !Number.isFinite(performance.timeOrigin) ||
        timestamp > completedAtMilliseconds + 1 ||
        completedAtMilliseconds < requestedAtMilliseconds ||
        (side === "before"
          ? timestamp < cutoff - bracketMilliseconds || timestamp >= cutoff
          : timestamp < cutoff || timestamp > cutoff + bracketMilliseconds)
      )
        return fail(state, "stats-outside-boundary");
      if (side === "before") return;
      const before = state.before;
      if (!before) return fail(state, "missing-before-snapshot");
      if (before.id !== sample.id || before.ssrc !== sample.ssrc)
        return fail(state, "inbound-stream-changed");
      if (sample.framesDecoded < before.framesDecoded)
        return fail(state, "decoded-counter-reset");
      if (!counters.every((name) => sample[name] === before[name]))
        return fail(state, "counters-changed-across-boundary");
      state.status = "valid";
      clearTimers();
    } catch {
      // Keep raw browser errors and any network identifiers out of artifacts.
      fail(state, "stats-request-failed");
    } finally {
      pending = false;
    }
  };

  window.__rstreamTransitionBoundary = {
    observe(phase) {
      if (stopped || !phase) return;
      if (
        current?.phase === phase.name &&
        current?.phaseStartedAt === phase.startedAt
      )
        return;
      clearTimers();
      current = null;
      if (!phases.has(phase.name)) return;
      const observedAtMilliseconds = performance.now();
      const peer = activePeer();
      const state = {
        phase: phase.name,
        phaseStartedAt: phase.startedAt,
        observedAtMilliseconds,
        cutoffMilliseconds: observedAtMilliseconds + graceMilliseconds,
        status: "pending",
        reason: null,
        before: null,
        after: null,
        peer,
      };
      current = state;
      if (!peer) return fail(state, "missing-peer");
      if (pending) return fail(state, "previous-stats-request-pending");
      schedule(
        state.cutoffMilliseconds - 150,
        () => void capture(state, "before"),
      );
      schedule(
        state.cutoffMilliseconds + 100,
        () => void capture(state, "after"),
      );
      schedule(state.cutoffMilliseconds + bracketMilliseconds, () =>
        fail(state, "boundary-deadline-exceeded"),
      );
    },
    snapshot() {
      if (!current) return null;
      const { peer, ...record } = current;
      return {
        schemaVersion: 1,
        scope:
          "Native counters around four seconds after first browser phase observation",
        graceMilliseconds,
        bracketMilliseconds,
        timeOriginMilliseconds: performance.timeOrigin,
        ...record,
      };
    },
    stop() {
      if (stopped) return;
      fail(current, "stopped");
      stopped = true;
      clearTimers();
    },
  };
}
