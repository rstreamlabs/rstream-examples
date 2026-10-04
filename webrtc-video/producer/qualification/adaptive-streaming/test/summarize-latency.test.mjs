import assert from "node:assert/strict";
import test from "node:test";
import { summarizeLatency } from "../latency/summarize-latency.mjs";

const clocks = {
  producerBootHash: "c".repeat(64),
  receiverBootHash: "c".repeat(64),
  producerMonotonicOffsetHash: "d".repeat(64),
  receiverMonotonicOffsetHash: "d".repeat(64),
  ...Object.fromEntries(
    ["initialCalibration", "finalCalibration"].map((key, side) => [
      key,
      Array.from({ length: 7 }, (_, i) => ({
        hostBeforeMilliseconds: 10000 + side * 10000 + i * 10,
        hostAfterMilliseconds: 10002 + side * 10000 + i * 10,
        browserNowMilliseconds: 10001 + side * 10000 + i * 10,
        browserTimeOriginMilliseconds: 1790000000000,
      })),
    ]),
  ),
};
const sample = (source, latency) => ({
  sourceMonotonicMilliseconds: source,
  expectedDisplayMonotonicMilliseconds: source + latency,
  expectedDisplayPerformanceMilliseconds: source + latency,
  browserTimeOriginMilliseconds: 1790000000000,
  latencyMilliseconds: latency,
  sampledWallClockOffsetMilliseconds: 0,
});
const snapshot = (samples, overrides = {}) => ({
  samples,
  rejected: 0,
  omitted: 0,
  maximumProbeMilliseconds: 1,
  ...overrides,
});

test("reports the complete latency distribution without hiding high samples", () => {
  const values = Array.from({ length: 100 }, (_, index) =>
    sample(1000 + index * 200, 100 + index),
  );
  const report = summarizeLatency([snapshot(values)], clocks);
  assert.equal(report.measurementValid, true);
  assert.deepEqual(report.latencyMilliseconds, {
    minimum: 100,
    median: 149,
    p95: 194,
    p99: 198,
    maximum: 199,
  });
  assert.equal(report.samples, 100);
  assert.equal(report.maximumProbeMilliseconds, 1);
});

test("rejects incompatible clocks while wall-clock corrections cannot alter monotonic latency", () => {
  const values = [sample(1000, 100), sample(1200, 100)];
  const options = { minimumSamples: 2 };
  const incompatible = summarizeLatency(
    [snapshot(values)],
    {
      ...clocks,
      receiverBootHash: "d".repeat(64),
    },
    options,
  );
  assert.equal(incompatible.measurementValid, false);
  assert.equal(incompatible.gates.sharedHostClock, false);
  values[1].sampledWallClockOffsetMilliseconds = 20;
  const jumped = summarizeLatency([snapshot(values)], clocks, options);
  assert.equal(jumped.measurementValid, true);
  assert.equal(jumped.gates.clockAgreement, true);
  assert.equal(jumped.latencyMilliseconds.median, 100);
  assert.equal(jumped.maximumWallClockOffsetMilliseconds, 20);
  const drifted = summarizeLatency(
    [snapshot(values)],
    {
      ...clocks,
      finalCalibration: clocks.finalCalibration.map((entry) => ({
        ...entry,
        browserNowMilliseconds: entry.browserNowMilliseconds - 20,
      })),
    },
    options,
  );
  assert.equal(drifted.measurementValid, false);
  assert.equal(drifted.gates.clockAgreement, false);
  const namespace = summarizeLatency(
    [snapshot(values)],
    { ...clocks, receiverMonotonicOffsetHash: "e".repeat(64) },
    options,
  );
  assert.equal(namespace.gates.sharedHostClock, false);
});

test("keeps negative, corrupt, missing and omitted observations visible", () => {
  const options = { minimumSamples: 1 };
  const negative = summarizeLatency(
    [snapshot([sample(1000, -1)])],
    clocks,
    options,
  );
  assert.equal(negative.measurementValid, false);
  assert.equal(negative.latencyMilliseconds.minimum, -1);
  const lost = summarizeLatency(
    [
      snapshot([sample(1000, 100)], {
        rejected: 2,
        omitted: 3,
      }),
      null,
    ],
    clocks,
    options,
  );
  assert.equal(lost.measurementValid, false);
  assert.equal(lost.rejected, 2);
  assert.equal(lost.omitted, 3);
  assert.equal(lost.malformed, 1);
  const corrupt = sample(1000, 100);
  corrupt.expectedDisplayMonotonicMilliseconds = 9999;
  assert.equal(
    summarizeLatency([snapshot([corrupt])], clocks, options).gates
      .validTimestamps,
    false,
  );
  const empty = summarizeLatency([], clocks);
  assert.equal(empty.measurementValid, false);
  assert.equal(empty.latencyMilliseconds.minimum, null);
  assert.equal(empty.latencyMilliseconds.p95, null);
  const misaligned = sample(1000, 100);
  misaligned.expectedDisplayPerformanceMilliseconds += 10;
  assert.equal(
    summarizeLatency([snapshot([misaligned])], clocks, options).gates
      .clockAgreement,
    false,
  );
});

test("detects source-clock reversal instead of sorting it away", () => {
  const report = summarizeLatency(
    [snapshot([sample(1200, 100), sample(1000, 100)])],
    clocks,
    {
      minimumSamples: 2,
    },
  );
  assert.equal(report.measurementValid, false);
  assert.equal(report.gates.monotonicSource, false);
});
