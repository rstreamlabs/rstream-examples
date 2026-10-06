import { summarizeClockAlignment } from "./clock.mjs";

// Offline qualification report. Preserve rejected/omitted measurements and
// clock failures; never turn missing or negative latency into a zero value.
export function summarizeLatency(
  snapshots,
  clocks,
  { minimumSamples = 50, maximumClockUncertaintyMilliseconds = 5 } = {},
) {
  if (
    !Number.isInteger(minimumSamples) ||
    minimumSamples < 1 ||
    !Number.isFinite(maximumClockUncertaintyMilliseconds) ||
    maximumClockUncertaintyMilliseconds <= 0
  )
    throw new TypeError("Invalid latency qualification bounds");
  const samples = [];
  let rejected = 0,
    omitted = 0,
    malformed = 0,
    maximumProbeMilliseconds = 0;
  for (const snapshot of snapshots) {
    if (
      !snapshot ||
      !Array.isArray(snapshot.samples) ||
      !Number.isInteger(snapshot.rejected) ||
      snapshot.rejected < 0 ||
      !Number.isInteger(snapshot.omitted) ||
      snapshot.omitted < 0 ||
      !Number.isFinite(snapshot.maximumProbeMilliseconds) ||
      snapshot.maximumProbeMilliseconds < 0
    ) {
      malformed++;
      continue;
    }
    samples.push(...snapshot.samples);
    rejected += snapshot.rejected;
    omitted += snapshot.omitted;
    maximumProbeMilliseconds = Math.max(
      maximumProbeMilliseconds,
      snapshot.maximumProbeMilliseconds,
    );
  }
  const valid = samples.filter(
    (sample) =>
      sample &&
      [
        sample.sourceMonotonicMilliseconds,
        sample.expectedDisplayMonotonicMilliseconds,
        sample.expectedDisplayPerformanceMilliseconds,
        sample.browserTimeOriginMilliseconds,
        sample.latencyMilliseconds,
        sample.sampledWallClockOffsetMilliseconds,
      ].every(Number.isFinite),
  );
  malformed += samples.length - valid.length;
  const latencies = valid
    .map((sample) => sample.latencyMilliseconds)
    .sort((a, b) => a - b);
  const percentile = (fraction) =>
    latencies[Math.max(0, Math.ceil(latencies.length * fraction) - 1)] ?? null;
  const maximumClockOffset = valid.length
    ? valid.reduce(
        (maximum, sample) =>
          Math.max(
            maximum,
            Math.abs(sample.sampledWallClockOffsetMilliseconds),
          ),
        0,
      )
    : null;
  const decodedMarkerFraction =
    valid.length + rejected + malformed > 0
      ? valid.length / (valid.length + rejected + malformed)
      : 0;
  const alignment = summarizeClockAlignment(
    clocks?.initialCalibration,
    clocks?.finalCalibration,
    maximumClockUncertaintyMilliseconds,
  );
  const gates = {
    sharedHostClock:
      typeof clocks?.producerBootHash === "string" &&
      /^[a-f0-9]{64}$/.test(clocks.producerBootHash) &&
      clocks.producerBootHash === clocks.receiverBootHash &&
      typeof clocks.producerMonotonicOffsetHash === "string" &&
      /^[a-f0-9]{64}$/.test(clocks.producerMonotonicOffsetHash) &&
      clocks.producerMonotonicOffsetHash === clocks.receiverMonotonicOffsetHash,
    enoughSamples: valid.length >= minimumSamples,
    clockAgreement:
      alignment.valid &&
      valid.every(
        (sample) =>
          sample.browserTimeOriginMilliseconds ===
            alignment.initial.browserTimeOriginMilliseconds &&
          Math.abs(
            sample.expectedDisplayMonotonicMilliseconds -
              sample.expectedDisplayPerformanceMilliseconds -
              alignment.offsetMilliseconds,
          ) <= 0.001,
      ),
    validTimestamps:
      malformed === 0 &&
      valid.every(
        (sample) =>
          sample.sourceMonotonicMilliseconds > 0 &&
          sample.latencyMilliseconds >= 0 &&
          Math.abs(
            sample.latencyMilliseconds -
              (sample.expectedDisplayMonotonicMilliseconds -
                sample.sourceMonotonicMilliseconds),
          ) <= 0.001,
      ),
    monotonicSource: valid.every(
      (sample, index) =>
        index === 0 ||
        sample.sourceMonotonicMilliseconds >=
          valid[index - 1].sourceMonotonicMilliseconds,
    ),
    markersReadable: decodedMarkerFraction >= 0.95,
    noOmittedSamples: omitted === 0,
  };
  return {
    measurementValid: Object.values(gates).every(Boolean),
    scope:
      "Raw-frame monotonic marker before encoding to expected browser composition on one Linux clock with bounded browser alignment; excludes physical exposure and display scanout.",
    gates,
    samples: valid.length,
    rejected,
    omitted,
    malformed,
    decodedMarkerFraction,
    clockAlignment: alignment,
    maximumWallClockOffsetMilliseconds: maximumClockOffset,
    maximumProbeMilliseconds,
    latencyMilliseconds: {
      minimum: latencies[0] ?? null,
      median: percentile(0.5),
      p95: percentile(0.95),
      p99: percentile(0.99),
      maximum: latencies.at(-1) ?? null,
    },
  };
}
