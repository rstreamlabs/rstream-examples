import assert from "node:assert/strict";
import test from "node:test";
import { createLatencyReport } from "../latency/report.mjs";

const clocks = {
  producerBootHash: "c".repeat(64),
  receiverBootHash: "c".repeat(64),
};

function phase(name, start, latency = 100) {
  return Array.from({ length: 14 }, (_, index) => ({
    phase: name,
    latency: {
      samples: Array.from({ length: 5 }, (_, frame) => {
        const source = start + index * 1000 + frame * 200;
        return {
          sourceUnixMilliseconds: source,
          expectedDisplayUnixMilliseconds: source + latency,
          latencyMilliseconds: latency,
          sampledClockOffsetMilliseconds: 0,
        };
      }),
      rejected: 0,
      omitted: 0,
      maximumProbeMilliseconds: 1,
    },
  }));
}

test("isolates phase intervals while retaining transition latency in the overall distribution", () => {
  const snapshots = [
    ...phase("baseline", 1000),
    ...phase("source-network", 15000, 200),
  ];
  for (const index of [0, 13, 14, 27]) {
    for (const sample of snapshots[index].latency.samples) {
      sample.latencyMilliseconds = 1000;
      sample.expectedDisplayUnixMilliseconds =
        sample.sourceUnixMilliseconds + 1000;
    }
  }
  const report = createLatencyReport(snapshots, clocks, true);
  assert.equal(report.measurementValid, true);
  assert.equal(report.samples, 140);
  assert.equal(report.latencyMilliseconds.maximum, 1000);
  assert.equal(report.phases.baseline.samples, 60);
  assert.equal(report.phases.baseline.latencyMilliseconds.maximum, 100);
  assert.equal(
    report.phases["source-network"].latencyMilliseconds.maximum,
    200,
  );
});

test("rejects incomplete runs, absent baselines, undersampled phases and incompatible clocks", () => {
  const snapshots = phase("baseline", 1000);
  assert.equal(
    createLatencyReport(snapshots, clocks, false).measurementValid,
    false,
  );
  assert.equal(
    createLatencyReport(phase("recovery", 1000), clocks, true).measurementValid,
    false,
  );
  const short = createLatencyReport(
    [...snapshots, ...phase("recovery", 15000).slice(0, 3)],
    clocks,
    true,
  );
  assert.equal(short.measurementValid, false);
  assert.equal(short.phases.recovery.gates.enoughSamples, false);
  assert.equal(
    createLatencyReport(
      snapshots,
      { ...clocks, receiverBootHash: "d".repeat(64) },
      true,
    ).measurementValid,
    false,
  );
});

test("does not hide invalid or omitted observations at a phase boundary", () => {
  const snapshots = phase("baseline", 1000);
  snapshots[0].latency.omitted = 1;
  snapshots.at(-1).latency = null;
  const report = createLatencyReport(snapshots, clocks, true);
  assert.equal(report.phases.baseline.measurementValid, true);
  assert.equal(report.measurementValid, false);
  assert.equal(report.omitted, 1);
  assert.equal(report.malformed, 1);
});
