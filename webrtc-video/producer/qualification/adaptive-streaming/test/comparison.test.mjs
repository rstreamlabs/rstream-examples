import assert from "node:assert/strict";
import test from "node:test";
import {
  compare,
  renderComparisonMarkdown,
  renderComparisonSVGs,
} from "../lib/comparison.mjs";

test("accepts a full profile that closes the direct/relay quality gap", () => {
  const runs = [
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  const result = compare(runs);
  assert.equal(result.passed, true, JSON.stringify(result.assertions));
  const markdown = renderComparisonMarkdown(result);
  assert.match(markdown, /full protection profile/);
  assert.match(markdown, /2 FlexFEC repair packets per 4 media packets/);
  const figures = renderComparisonSVGs(result);
  assert.deepEqual(Object.keys(figures), [
    "comparison-decoded-output.svg",
    "comparison-quantization.svg",
    "comparison-frozen-time.svg",
  ]);
  assert.match(figures["comparison-decoded-output.svg"], /Decoded output/);
  assert.match(figures["comparison-decoded-output.svg"], /20 fps gate/);
  assert.match(figures["comparison-decoded-output.svg"], /median and min–max/);
  assert.match(figures["comparison-decoded-output.svg"], /width="960" height="440"/);
  assert.match(figures["comparison-decoded-output.svg"], /font-family:system-ui,sans-serif/);
  assert.match(figures["comparison-quantization.svg"], /QP 42 gate/);
  assert.match(figures["comparison-frozen-time.svg"], /10% gate/);
  for (const svg of Object.values(figures)) {
    assert.doesNotMatch(svg, />PASS</);
  }
});

test("rejects an incomplete or degraded full relay profile", () => {
  const result = compare([
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", false, 14, 0.25),
    { directory: "broken", error: "missing summary" },
  ]);
  assert.equal(result.passed, false);
  assert.equal(
    result.assertions.find((assertion) => assertion.name === "full-relay-pass")
      .passed,
    false,
  );
  assert.equal(
    result.assertions.find((assertion) => assertion.name === "complete-runs")
      .passed,
    false,
  );
});

test("rejects mixed revisions and a dirty producer tree", () => {
  const runs = [
    run("direct", "nack-rtx", true, 28, 0.01),
    run("relay", "nack-rtx", true, 27, 0.02),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs[0].manifest.git.revision = "another-revision";
  runs[1].manifest.git.dirty = true;
  const result = compare(runs);
  assert.equal(result.passed, false);
  assert.equal(assertion(result, "single-revision").passed, false);
  assert.equal(assertion(result, "clean-producer-tree").passed, false);
});

test("rejects different producer or browser images", () => {
  const runs = [
    run("direct", "nack-rtx", true, 28, 0.01),
    run("relay", "nack-rtx", true, 27, 0.02),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs[0].manifest.producerImage = "producer-image-changed";
  runs[1].manifest.browserImage = "browser-image-changed";
  const result = compare(runs);
  assert.equal(result.passed, false);
  assert.equal(assertion(result, "single-producer-image").passed, false);
  assert.equal(assertion(result, "single-browser-image").passed, false);
});

test("rejects mixed FlexFEC ratios in one full-profile matrix", () => {
  const runs = [
    run("direct", "nack-rtx", true, 28, 0.01),
    run("relay", "nack-rtx", true, 27, 0.02),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs.at(-1).manifest.protection.flexFECRepairPackets = 1;
  const result = compare(runs);
  assert.equal(result.passed, false);
  assert.equal(
    assertion(result, "single-full-protection-profile").passed,
    false,
  );
});

test("compares impairment conditions without conflating path selectors", () => {
  const runs = [
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  for (const candidate of runs) {
    candidate.manifest.phases[0].shaping.scope =
      candidate.manifest.networkPath.kind === "direct"
        ? "peer-webrtc-transport-address"
        : "producer-turn-transport";
  }
  let result = compare(runs);
  assert.equal(assertion(result, "single-impairment-profile").passed, true);
  runs.at(-1).manifest.phases[0].shaping.loss = "3%";
  result = compare(runs);
  assert.equal(assertion(result, "single-impairment-profile").passed, false);
});

test("requires forced adaptation on the controlled direct reference", () => {
  const runs = [
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs.at(-2).summary.congestionResponseRequired = false;
  const result = compare(runs);
  assert.equal(result.passed, false);
  assert.equal(
    assertion(result, "full-direct-adaptation-coverage").passed,
    false,
  );
});

test("accepts valid relay feedback when its baseline already fits", () => {
  const runs = [
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs.at(-1).summary.congestionResponseRequired = false;
  const result = compare(runs);
  assert.equal(result.passed, true, JSON.stringify(result.assertions));
  assert.equal(assertion(result, "full-relay-feedback-coverage").passed, true);
});

test("rejects a relay run without valid controller feedback", () => {
  const runs = [
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 28, 0.014),
  ];
  runs
    .at(-1)
    .summary.assertions.find(
      (item) => item.name === "twcc-feedback-integrity",
    ).passed = false;
  const result = compare(runs);
  assert.equal(result.passed, false);
  assert.equal(assertion(result, "full-relay-feedback-coverage").passed, false);
});

test("rejects a visually degraded relay despite healthy frame delivery", () => {
  const result = compare([
    run("direct", "nack-rtx", true, 23, 0.07),
    run("relay", "nack-rtx", false, 17, 0.3),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006, 27),
    run("relay", "nack-rtx-flexfec", true, 29, 0.006, 49),
  ]);
  assert.equal(result.passed, false);
  assert.equal(
    assertion(result, "full-relay-compression-quality").passed,
    false,
  );
  assert.equal(assertion(result, "relay-direct-quality-gap").passed, false);
});

test("does not require an artificial gain over an already healthy baseline", () => {
  const result = compare([
    run("direct", "nack-rtx", true, 29, 0.01),
    run("relay", "nack-rtx", true, 28, 0.02),
    run("direct", "nack-rtx-flexfec", true, 29, 0.006),
    run("relay", "nack-rtx-flexfec", true, 27, 0.025),
  ]);
  assert.equal(result.passed, true, JSON.stringify(result.assertions));
  assert.equal(assertion(result, "proactive-repair-gain").passed, true);
});

function run(
  path,
  profile,
  passed,
  fps,
  freezeRatio,
  averageQP = path === "relay" ? 31 : 28,
) {
  return {
    directory: `${path}-${profile}`,
    manifest: {
      browserImage: "browser-image",
      git: {
        dirty: false,
        producerTree: "producer-tree",
        revision: "revision",
      },
      networkPath: { kind: path },
      producerImage: "producer-image",
      phases: [
        {
          name: "impaired",
          shaping: {
            capacityKbps: 1200,
            delay: "120ms",
            jitter: "30ms",
            loss: "2%",
          },
        },
      ],
      protection: {
        flexFEC: profile === "nack-rtx-flexfec",
        flexFECMediaPackets: profile === "nack-rtx-flexfec" ? 4 : 0,
        flexFECRepairPackets: profile === "nack-rtx-flexfec" ? 2 : 0,
        profile,
      },
    },
    summary: {
      assertions: [
        { name: "continued-pressure", passed: true },
        { name: "twcc-feedback-integrity", passed: true },
      ],
      congestionResponseRequired: true,
      passed,
      phases: {
        baseline: {
          medianEncoderTargetKbps: 5000,
        },
        impaired: {
          averageQP,
          decodedFramesPerSecond: fps,
          freezeRatio,
          maximumRTTMilliseconds: path === "relay" ? 300 : 200,
          medianEncoderTargetKbps: 2000,
          medianReceivedBitrateKbps: 1800,
        },
      },
    },
  };
}

function assertion(result, name) {
  const found = result.assertions.find((item) => item.name === name);
  assert.ok(found, `missing assertion ${name}`);
  return found;
}
