import assert from "node:assert/strict";
import test from "node:test";
import { summarizeSourceFormats } from "../formats/report.mjs";

const profiles = [
  {
    id: "small",
    width: 640,
    height: 360,
    frameRate: { numerator: 15, denominator: 1 },
  },
  {
    id: "medium",
    width: 960,
    height: 540,
    frameRate: { numerator: 24, denominator: 1 },
  },
  {
    id: "large",
    width: 1280,
    height: 720,
    frameRate: { numerator: 30, denominator: 1 },
  },
];

function fixture() {
  let frames = 0;
  return Array.from({ length: 121 }, (_, second) => {
    const phase =
      second < 30 ? "baseline" : second < 75 ? "source-network" : "recovery";
    const profile = profiles[second < 35 || second >= 95 ? 2 : 0];
    // The interval belongs to the preceding caps observation.
    if (second) frames += second <= 35 || second > 95 ? 30 : 15;
    return {
      phase,
      elapsedMilliseconds: second * 1000,
      videoCurrentTimeSeconds: second,
      framesDecoded: frames,
      frameWidth: profile.width,
      frameHeight: profile.height,
      peerConnectionsCreated: 1,
      whepSessionCreates: 1,
      whepRestartPatches: 0,
      sourceQuality: {
        modes: [
          { id: "auto", label: "Auto", bitrateKbps: 0 },
          { id: "low", label: "Low", bitrateKbps: 1000 },
        ],
        selected: "auto",
        version: "a".repeat(32) + ":1",
        activeEncoders: 1,
        minAppliedBitrateKbps: 1000,
        maxAppliedBitrateKbps: 1000,
        failedUpdates: 0,
        sourceFormat: {
          defaultProfile: "large",
          adaptive: true,
          activeEncoders: 1,
          pendingEncoders: 0,
          unconfirmedEncoders: 0,
          failedUpdates: 0,
          profiles: profiles.map((value) => ({
            ...value,
            requestedEncoders: +(value.id === profile.id),
            observedEncoders: +(value.id === profile.id),
          })),
        },
      },
    };
  });
}

test("automatic caps, decoded dimensions and configured cadence qualify a 30→15→30 fps cycle", () => {
  for (const networkPhase of ["source-network", "viewer-network"]) {
    const samples = fixture().map((sample) => ({
      ...sample,
      phase: sample.phase === "source-network" ? networkPhase : sample.phase,
    }));
    const result = summarizeSourceFormats(samples, "small");
    assert.equal(result.passed, true, JSON.stringify(result));
    assert.deepEqual(
      result.transitions.map((transition) => transition.profile),
      ["large", "small", "large"],
    );
    assert.equal(result.phases[networkPhase].steadyDecodedFramesPerSecond, 15);
    assert.equal(result.phases[networkPhase].configuredFrameRatio, 1);
    assert.equal(result.phases.recovery.configuredFrameRatio, 1);
    assert.equal(result.maximumSampleGapMilliseconds, 1000);
  }
});

test("a requested format is not proof of caps or decoded output", () => {
  const samples = fixture();
  for (const sample of samples.filter(
    (value) => value.phase === "source-network",
  )) {
    for (const profile of sample.sourceQuality.sourceFormat.profiles)
      profile.observedEncoders = +(profile.id === "large");
  }
  const result = summarizeSourceFormats(samples, "small");
  assert.equal(result.gates.expectedSteadyFormats, false);
  assert.equal(result.passed, false);
});

test("invalid or incomplete quality observations cannot qualify", () => {
  for (const change of [
    (sample) => {
      delete sample.sourceQuality;
    },
    (sample) => {
      sample.sourceQuality.selected = "low";
    },
    (sample) => {
      sample.sourceQuality.failedUpdates = 1;
    },
    (sample) => {
      sample.sourceQuality.sourceFormat.failedUpdates = 1;
    },
    (sample) => {
      sample.sourceQuality.sourceFormat.adaptive = false;
    },
    (sample) => {
      sample.sourceQuality.sourceFormat.activeEncoders = 0;
    },
  ]) {
    const samples = fixture();
    change(samples[60]);
    const result = summarizeSourceFormats(samples, "small");
    assert.equal(result.gates.qualityComplete, false);
    assert.equal(result.passed, false);
  }
  assert.equal(summarizeSourceFormats([], "small").passed, false);
});

test("steady output must have the expected dimensions, cadence and confirmed caps", () => {
  for (const change of [
    (sample) => {
      sample.frameWidth = 1280;
    },
    (sample) => {
      sample.sourceQuality.sourceFormat.pendingEncoders = 1;
    },
    (sample) => {
      sample.sourceQuality.sourceFormat.unconfirmedEncoders = 1;
      for (const profile of sample.sourceQuality.sourceFormat.profiles)
        profile.observedEncoders = 0;
    },
  ]) {
    const samples = fixture();
    change(samples[70]);
    assert.equal(
      summarizeSourceFormats(samples, "small").gates.expectedSteadyFormats,
      false,
    );
  }
  const samples = fixture();
  for (const sample of samples)
    sample.framesDecoded = Math.floor(sample.framesDecoded * 0.7);
  assert.equal(
    summarizeSourceFormats(samples, "small").gates.configuredCadence,
    false,
  );
});

test("sampling gaps, counter resets, media regression and a second session fail independently", () => {
  for (const [gate, change] of [
    [
      "samplingContinuous",
      (samples) => {
        samples.splice(55, 4);
      },
    ],
    [
      "sampledMediaContinuous",
      (samples) => {
        samples[60].videoCurrentTimeSeconds = 0;
      },
    ],
    [
      "decodedCountersContinuous",
      (samples) => {
        samples[60].framesDecoded = 0;
      },
    ],
    [
      "singleSession",
      (samples) => {
        samples[60].peerConnectionsCreated = 2;
      },
    ],
    [
      "singleSession",
      (samples) => {
        samples[60].whepSessionCreates = 2;
      },
    ],
    [
      "singleSession",
      (samples) => {
        samples[60].whepRestartPatches = 1;
      },
    ],
  ]) {
    const samples = fixture();
    change(samples);
    const result = summarizeSourceFormats(samples, "small");
    assert.equal(result.gates[gate], false, gate);
    assert.equal(result.passed, false);
  }
});

test("missing recovery, changing definitions, unexpected phases and rapid oscillation fail", () => {
  assert.equal(
    summarizeSourceFormats(
      fixture().filter((sample) => sample.phase !== "recovery"),
      "small",
    ).passed,
    false,
  );
  assert.equal(
    summarizeSourceFormats(fixture(), "large").gates.lowerNetworkFormat,
    false,
  );
  let samples = fixture();
  samples[60].sourceQuality.sourceFormat.defaultProfile = "medium";
  assert.equal(
    summarizeSourceFormats(samples, "small").gates.stableConfiguration,
    false,
  );
  samples = fixture();
  samples[60].phase = "baseline";
  assert.equal(
    summarizeSourceFormats(samples, "small").gates.phaseOrder,
    false,
  );
  samples = fixture();
  for (const sample of samples.slice(40, 42)) {
    for (const profile of sample.sourceQuality.sourceFormat.profiles)
      profile.observedEncoders = +(profile.id === "medium");
  }
  assert.equal(
    summarizeSourceFormats(samples, "small").gates.minimumDwell,
    false,
  );
});
