import assert from "node:assert/strict";
import test from "node:test";
import { summarizeLatency } from "../latency/summarize-latency.mjs";

const clocks = {
  producerBootHash: "c".repeat(64),
  receiverBootHash: "c".repeat(64),
};
const sample = (source, latency) => ({
  sourceUnixMilliseconds: source,
  expectedDisplayUnixMilliseconds: source + latency,
  latencyMilliseconds: latency,
  sampledClockOffsetMilliseconds: 0,
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

test("rejects incompatible clocks and clock jumps", () => {
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
  values[1].sampledClockOffsetMilliseconds = 20;
  const jumped = summarizeLatency([snapshot(values)], clocks, options);
  assert.equal(jumped.measurementValid, false);
  assert.equal(jumped.gates.clockAgreement, false);
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
  corrupt.expectedDisplayUnixMilliseconds = 9999;
  assert.equal(
    summarizeLatency([snapshot([corrupt])], clocks, options).gates
      .validTimestamps,
    false,
  );
  const empty = summarizeLatency([], clocks);
  assert.equal(empty.measurementValid, false);
  assert.equal(empty.latencyMilliseconds.minimum, null);
  assert.equal(empty.latencyMilliseconds.p95, null);
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
