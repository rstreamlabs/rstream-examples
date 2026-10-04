import { summarizeLatency } from "./summarize-latency.mjs";

// Keep phase transitions out of steady measurements: a drained snapshot spans
// the preceding collector interval. Complete/teardown is not a media phase.
export function createLatencyReport(snapshots, clocks, collectionComplete) {
  const phases = {};
  for (const phase of [
    "warmup",
    "baseline",
    "source-network",
    "viewer-network",
    "recovery",
  ]) {
    const selected = snapshots.filter((snapshot) => snapshot.phase === phase);
    if (selected.length === 0) continue;
    phases[phase] = summarizeLatency(
      selected.slice(1, -1).map((snapshot) => snapshot.latency),
      clocks,
    );
  }
  const overall = summarizeLatency(
    snapshots.map((snapshot) => snapshot.latency),
    clocks,
  );
  return {
    ...overall,
    enabled: true,
    clocks,
    collectionComplete,
    phaseBoundaryPolicy:
      "Exclude the first and last collected snapshot of each phase from phase summaries; retain them in the overall result.",
    phases,
    measurementValid:
      collectionComplete &&
      overall.measurementValid &&
      Object.hasOwn(phases, "baseline") &&
      Object.values(phases).every((phase) => phase.measurementValid),
  };
}
