import assert from "node:assert/strict";
import test from "node:test";
import {
  alignNetworkConditions,
  analyze,
  maximumEncoderTargetCoverage,
  parseEncoderQuality,
  renderMarkdown,
  renderSVG,
  samplesBeforeNetworkTransition,
  summarizeHostCPU,
  summarizeNetworkMobility,
  summarizeSetup,
} from "../lib/analysis.mjs";
import {
  renderNetworkConditionsSVG,
  renderPlaybackQualitySVG,
  renderTransportEvidenceSVG,
} from "../lib/evidence-svg.mjs";

test("aligns link changes to observed collector timestamps", () => {
  const startedAt = Date.parse("2026-08-16T10:00:00.000Z");
  const samples = [
    {
      capturedAt: new Date(startedAt).toISOString(),
      elapsedMilliseconds: 1000,
    },
    {
      capturedAt: new Date(startedAt + 60_000).toISOString(),
      elapsedMilliseconds: 61_000,
    },
  ];
  const events = [
    ["conditioning-started", 5000],
    ["constrained-started", 10_000],
    ["constrained-step-2-started", 15_000],
    ["constrained-step-3-started", 20_000],
    ["constrained-steady-started", 25_000],
    ["impaired-started", 30_000],
    ["recovery-started", 40_000],
    ["recovery-capacity-started", 45_000],
  ].map(([name, offset]) => ({
    name,
    observedAt: new Date(startedAt + offset).toISOString(),
  }));
  const manifest = {
    phases: [
      {
        name: "conditioning",
        shaping: {
          capacityKbps: 32_000,
          delay: "0ms",
          jitter: "0ms",
          loss: "0%",
        },
      },
      {
        name: "constrained",
        shaping: {
          schedule: [16_000, 12_000, 8000, 4000].map((capacityKbps) => ({
            capacityKbps,
            delay: "0ms",
            jitter: "0ms",
            loss: "0%",
          })),
        },
      },
      {
        name: "impaired",
        shaping: {
          capacityKbps: 4000,
          delay: "120ms",
          jitter: "30ms",
          loss: "2%",
        },
      },
      {
        name: "recovery",
        shaping: {
          schedule: [4000, 32_000].map((capacityKbps) => ({
            capacityKbps,
            delay: "0ms",
            jitter: "0ms",
            loss: "0%",
          })),
        },
      },
    ],
  };
  const timeline = alignNetworkConditions(events, samples, manifest);
  assert.equal(timeline.available, true);
  assert.deepEqual(
    timeline.changes.map((change) => change.elapsedMilliseconds),
    [6000, 11_000, 16_000, 21_000, 26_000, 31_000, 41_000, 46_000],
  );
  assert.equal(timeline.changes[4].capacityKbps, 4000);
  assert.equal(timeline.changes[5].lossPercent, 2);
  const chart = renderNetworkConditionsSVG(
    { networkConditions: timeline, passed: true, samples },
    manifest,
  );
  assert.match(
    chart,
    /Applied capacity · 32 → 16 → 12 → 8\.0 → 4\.0 → 32 Mb\/s/,
  );
  assert.match(
    chart,
    /Impaired interval · 120 ms one-way delay · 30 ms jitter · 2\.0% random loss/,
  );
  assert.match(chart, />0 s<\/text>/);
  assert.equal(
    alignNetworkConditions(events.slice(0, -1), samples, manifest).available,
    false,
  );
});

test("separates a stable conditioning window from the next link transition", () => {
  const samples = Array.from({ length: 16 }, (_, index) => ({
    elapsedMilliseconds: index * 1000,
    encoderTargetKbps: index === 14 ? 4000 : 8000,
    phase: "conditioning",
  }));
  const networkConditions = {
    changes: [{ elapsedMilliseconds: 15_000, name: "constrained-started" }],
  };
  const stable = samplesBeforeNetworkTransition(
    samples,
    networkConditions,
    "conditioning",
    "constrained-started",
    10_000,
    2000,
  );
  assert.deepEqual(
    stable.map((sample) => sample.elapsedMilliseconds),
    [3000, 4000, 5000, 6000, 7000, 8000, 9000, 10_000, 11_000, 12_000, 13_000],
  );
  assert.ok(stable.every((sample) => sample.encoderTargetKbps === 8000));
  assert.deepEqual(
    samplesBeforeNetworkTransition(
      samples,
      { changes: [] },
      "conditioning",
      "constrained-started",
    ),
    [],
  );
});

test("measures recovery residency without hiding sustained oscillation", () => {
  const samples = Array.from({ length: 11 }, (_, index) => ({
    elapsedMilliseconds: index * 1000,
    encoderTargetKbps: index === 8 || index === 9 ? 5000 : 8000,
    phase: "recovery",
  }));
  assert.equal(
    maximumEncoderTargetCoverage(samples, "recovery", 6400, 10_000),
    9 / 11,
  );
  assert.ok(
    maximumEncoderTargetCoverage(
      Array.from({ length: 31 }, (_, index) => ({
        elapsedMilliseconds: index * 1000,
        encoderTargetKbps: index % 2 === 0 ? 8000 : 5000,
        phase: "recovery",
      })),
      "recovery",
      6400,
      10_000,
    ) < 0.8,
  );
  assert.equal(
    maximumEncoderTargetCoverage(
      samples.slice(0, 10),
      "recovery",
      6400,
      10_000,
    ),
    0,
  );
});

test("separates image builds from service establishment", () => {
  const setup = summarizeSetup([
    { name: "producer-build-started", observedAt: "2026-08-14T10:00:00.000Z" },
    {
      name: "producer-build-completed",
      observedAt: "2026-08-14T10:01:00.000Z",
    },
    { name: "connection-started", observedAt: "2026-08-14T10:01:01.000Z" },
    { name: "producer-ready", observedAt: "2026-08-14T10:01:03.000Z" },
    { name: "media-connected", observedAt: "2026-08-14T10:01:06.250Z" },
  ]);
  assert.equal(setup.connectionMilliseconds, 5250);
  assert.equal(setup.milestones[1].sincePreviousMilliseconds, 60_000);
});

test("summarizes one bounded Trickle ICE mobility event", () => {
  const samples = [
    {
      elapsedMilliseconds: 10_000,
      localCandidateAddress: "192.0.2.10",
      localCandidatePort: 50_000,
      localCandidateProtocol: "udp",
      peerConnectionState: "connected",
      peerConnectionsCreated: 1,
      phase: "baseline",
      playback: "Playing",
      remoteCandidateAddress: "192.0.2.20",
      remoteCandidatePort: 60_000,
      remoteCandidateProtocol: "udp",
      remoteCandidatesReceived: 2,
      webSocketCloseCount: 0,
      webSocketsCreated: 1,
    },
    {
      elapsedMilliseconds: 11_000,
      peerConnectionState: "disconnected",
      peerConnectionsCreated: 1,
      phase: "mobility",
      playback: "Buffering",
      remoteCandidatesReceived: 2,
      webSocketCloseCount: 0,
      webSocketsCreated: 1,
    },
    {
      elapsedMilliseconds: 14_000,
      iceRestartOffers: 1,
      localCandidateAddress: "192.0.2.11",
      localCandidatePort: 50_001,
      localCandidateProtocol: "udp",
      peerConnectionState: "connected",
      peerConnectionsCreated: 1,
      phase: "mobility",
      playback: "Playing",
      remoteCandidateAddress: "192.0.2.21",
      remoteCandidatePort: 60_001,
      remoteCandidateProtocol: "udp",
      remoteCandidatesReceived: 3,
      webSocketCloseCount: 0,
      webSocketsCreated: 1,
    },
  ];
  assert.deepEqual(summarizeNetworkMobility(samples), {
    available: true,
    candidatePairSwitches: 1,
    iceRestartOffers: 1,
    maximumUnavailableMilliseconds: 3000,
    peerConnectionsCreated: 1,
    trickledRemoteCandidates: 1,
    webSocketCloses: 0,
    webSocketsCreated: 1,
  });
});

test("reports an unfinished mobility outage instead of hiding it", () => {
  const mobility = summarizeNetworkMobility([
    {
      elapsedMilliseconds: 1000,
      peerConnectionState: "connected",
      phase: "mobility",
      playback: "Playing",
    },
    {
      elapsedMilliseconds: 3000,
      peerConnectionState: "disconnected",
      phase: "mobility",
      playback: "Buffering",
    },
    {
      elapsedMilliseconds: 9000,
      peerConnectionState: "disconnected",
      phase: "mobility",
      playback: "Buffering",
    },
  ]);
  assert.equal(mobility.maximumUnavailableMilliseconds, 6000);
});

test("attributes host CPU execution and hypervisor steal time by phase", () => {
  const startedAt = Date.parse("2026-08-14T10:00:00.000Z");
  const samples = [];
  let userTicks = 0;
  let idleTicks = 0;
  let stealTicks = 0;
  for (let index = 0; index <= 40; index += 1) {
    if (index > 0) {
      userTicks += 50;
      idleTicks += 45;
      stealTicks += 5;
    }
    samples.push({
      capturedAt: new Date(startedAt + index * 1000).toISOString(),
      gapMilliseconds: index === 0 ? 0 : 250,
      idleTicks,
      ioWaitTicks: 0,
      irqTicks: 0,
      niceTicks: 0,
      softIRQTicks: 0,
      stealTicks,
      systemTicks: 0,
      userTicks,
    });
  }
  const summary = summarizeHostCPU(
    samples,
    [
      { name: "baseline", startedAt: new Date(startedAt).toISOString() },
      {
        name: "recovery",
        startedAt: new Date(startedAt + 20_000).toISOString(),
      },
    ],
    ["baseline", "recovery"],
  );
  assert.equal(summary.available, true);
  assert.equal(summary.phases.baseline.samples, 19);
  assert.equal(summary.phases.recovery.samples, 20);
  assert.equal(summary.phases.baseline.activeMedianRatio, 0.5);
  assert.equal(summary.phases.baseline.samplingGapMaximumMilliseconds, 250);
  assert.equal(summary.phases.baseline.samplingGapP99Milliseconds, 250);
  assert.equal(summary.phases.baseline.stealP95Ratio, 0.05);
});

test("requires scheduler evidence from newly recorded runtimes", () => {
  const result = analyze([], {
    networkPath: { icePolicy: "relay" },
    phases: [{ name: "baseline", shaping: null }],
    runtime: { logicalCPUs: 2 },
  });
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "producer-host-scheduler",
    ).passed,
    false,
  );
});

test("rejects receiver scheduling noise as an invalid transport run", () => {
  const startedAt = Date.parse("2026-08-14T10:00:00.000Z");
  const samples = [];
  let idleTicks = 0;
  let stealTicks = 0;
  let userTicks = 0;
  for (let index = 0; index <= 20; index += 1) {
    if (index > 0) {
      idleTicks += 40;
      stealTicks += 20;
      userTicks += 40;
    }
    samples.push({
      capturedAt: new Date(startedAt + index * 1000).toISOString(),
      gapMilliseconds: index === 0 ? 0 : 250,
      idleTicks,
      ioWaitTicks: 0,
      irqTicks: 0,
      niceTicks: 0,
      softIRQTicks: 0,
      stealTicks,
      systemTicks: 0,
      userTicks,
    });
  }
  const phaseTimeline = [
    { name: "baseline", startedAt: new Date(startedAt).toISOString() },
  ];
  const result = analyze(
    [],
    {
      browserRuntime: { logicalCPUs: 2 },
      networkPath: { icePolicy: "relay" },
      phases: [{ name: "baseline", shaping: null }],
    },
    null,
    null,
    null,
    null,
    null,
    null,
    phaseTimeline,
    samples,
  );
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "receiver-host-scheduler",
    ).passed,
    false,
  );
});

test("rejects a host sampler stall even when steal counters stay healthy", () => {
  const startedAt = Date.parse("2026-08-14T10:00:00.000Z");
  const samples = [];
  let idleTicks = 0;
  let userTicks = 0;
  for (let index = 0; index <= 20; index += 1) {
    if (index > 0) {
      idleTicks += 50;
      userTicks += 50;
    }
    samples.push({
      capturedAt: new Date(startedAt + index * 250).toISOString(),
      gapMilliseconds: index === 10 ? 500 : index === 0 ? 0 : 250,
      idleTicks,
      ioWaitTicks: 0,
      irqTicks: 0,
      niceTicks: 0,
      softIRQTicks: 0,
      stealTicks: 0,
      systemTicks: 0,
      userTicks,
    });
  }
  const result = analyze(
    [],
    {
      networkPath: { icePolicy: "relay" },
      phases: [{ name: "baseline", shaping: null }],
      runtime: { logicalCPUs: 2 },
    },
    null,
    null,
    null,
    null,
    null,
    samples,
    [{ name: "baseline", startedAt: new Date(startedAt).toISOString() }],
  );
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "producer-host-scheduler",
    ).passed,
    false,
  );
});

test("assigns timestamped x264 quality evidence to its active phase", () => {
  const log = [
    "2026-08-14T10:00:00.500000000Z 0:00:01.000 encoder frame=   1 QP=24.50 NAL=2 size=1200 bytes",
    "2026-08-14T10:00:01.500000000Z 0:00:01.033 encoder frame=   2 QP=25.50 NAL=2 size=1300 bytes",
    "2026-08-14T10:00:02.500000000Z 0:00:01.066 encoder frame=   3 QP=31.00 NAL=2 size=900 bytes",
    "2026-08-14T10:00:04.500000000Z 0:00:01.099 encoder frame=   4 QP=20.00 NAL=2 size=1500 bytes",
  ].join("\n");
  const quality = parseEncoderQuality(log, [
    { name: "connecting", startedAt: "2026-08-14T09:59:59.000Z" },
    { name: "baseline", startedAt: "2026-08-14T10:00:00.000Z" },
    { name: "impaired", startedAt: "2026-08-14T10:00:02.000Z" },
    { name: "complete", startedAt: "2026-08-14T10:00:04.000Z" },
  ]);
  assert.deepEqual(quality, {
    baseline: {
      averageQP: 25,
      burstFrameRatio: 0,
      encodedBytes: 2500,
      frameGapP99Milliseconds: 33,
      frameIntervals: 1,
      frames: 2,
      lateFrameRatio: 0,
      maximumFrameGapMilliseconds: 33,
    },
    impaired: {
      averageQP: 31,
      burstFrameRatio: 0,
      encodedBytes: 900,
      frameGapP99Milliseconds: 0,
      frameIntervals: 0,
      frames: 1,
      lateFrameRatio: 0,
      maximumFrameGapMilliseconds: 0,
    },
  });
});

test("maps a direct encoder evidence file from process monotonic time", () => {
  const log = [
    "0:00:01.000 encoder frame= 1 QP=24.00 NAL=2 size=1200 bytes",
    "0:00:01.100 encoder frame= 2 QP=26.00 NAL=2 size=1300 bytes",
  ].join("\n");
  const quality = parseEncoderQuality(
    log,
    [{ name: "baseline", startedAt: "2026-08-14T10:00:00.500Z" }],
    "2026-08-14T10:00:00.000Z",
    30,
  );
  assert.equal(quality.baseline.averageQP, 25);
  assert.equal(quality.baseline.maximumFrameGapMilliseconds, 100);
  assert.equal(quality.baseline.lateFrameRatio, 1);
});

test("separates isolated encoder jitter from sustained cadence failures", () => {
  const nominal = {
    averageQP: 25,
    burstFrameRatio: 0,
    encodedBytes: 100_000,
    frameGapP99Milliseconds: 40,
    frameIntervals: 30,
    frames: 31,
    lateFrameRatio: 0,
    maximumFrameGapMilliseconds: 44,
  };
  const manifest = {
    networkPath: { icePolicy: "direct" },
    phases: ["warmup", "baseline", "constrained", "impaired", "recovery"].map(
      (name) => ({ name, shaping: null }),
    ),
  };
  const samples = manifest.phases.map(({ name }, index) => ({
    bytesReceived: index * 1_000,
    elapsedMilliseconds: (index + 1) * 1_000,
    phase: name,
  }));
  const startupTransient = {
    ...nominal,
    maximumFrameGapMilliseconds: 500,
  };
  const isolatedJitter = {
    ...nominal,
    burstFrameRatio: 0.002,
    frameIntervals: 1_000,
    frames: 1_001,
    lateFrameRatio: 0.001,
    maximumFrameGapMilliseconds: 128,
  };
  const excessiveStall = {
    ...isolatedJitter,
    maximumFrameGapMilliseconds: 201,
  };
  const sustainedJitter = {
    ...isolatedJitter,
    frameGapP99Milliseconds: 51,
  };
  let result = analyze(samples, manifest, null, {
    baseline: nominal,
    constrained: nominal,
    impaired: nominal,
    recovery: nominal,
    warmup: startupTransient,
  });
  assert.equal(
    result.assertions.find((assertion) => assertion.name === "encoder-cadence")
      .passed,
    true,
  );
  result = analyze(samples, manifest, null, {
    baseline: nominal,
    constrained: nominal,
    impaired: nominal,
    recovery: isolatedJitter,
    warmup: nominal,
  });
  assert.equal(
    result.assertions.find((assertion) => assertion.name === "encoder-cadence")
      .passed,
    true,
  );
  for (const recovery of [excessiveStall, sustainedJitter]) {
    result = analyze(samples, manifest, null, {
      baseline: nominal,
      constrained: nominal,
      impaired: nominal,
      recovery,
      warmup: nominal,
    });
    assert.equal(
      result.assertions.find(
        (assertion) => assertion.name === "encoder-cadence",
      ).passed,
      false,
    );
  }
  for (const recovery of [
    { ...isolatedJitter, lateFrameRatio: 0.011 },
    { ...isolatedJitter, burstFrameRatio: 0.011 },
  ]) {
    result = analyze(samples, manifest, null, {
      baseline: nominal,
      constrained: nominal,
      impaired: nominal,
      recovery,
      warmup: nominal,
    });
    assert.equal(
      result.assertions.find(
        (assertion) => assertion.name === "encoder-cadence",
      ).passed,
      false,
    );
  }
});

test("accepts a continuous relay stream that reacts and recovers", () => {
  const phases = [
    ["warmup", 5000, 4200, 0],
    ["baseline", 5000, 4000, 0],
    ["constrained", 2500, 2100, 2500],
    ["impaired", 1800, 1550, 1800],
    ["recovery", 4000, 3500, 0],
  ];
  const samples = [];
  let elapsedMilliseconds = 0;
  let bytesReceived = 0;
  let framesDecoded = 0;
  let nackCount = 0;
  let packetsReceived = 0;
  let qpSum = 0;
  let jitterBufferDelaySeconds = 0;
  let jitterBufferEmittedCount = 0;
  let jitterBufferTargetDelaySeconds = 0;
  let retransmittedPacketsReceived = 0;
  let pacerSentRTX = 0;
  let totalDecodeTimeSeconds = 0;
  for (const [phase, encoderTargetKbps, receivedBitrateKbps] of phases) {
    for (let index = 0; index < 20; index += 1) {
      elapsedMilliseconds += 1000;
      bytesReceived += (receivedBitrateKbps * 1000) / 8;
      framesDecoded += 30;
      qpSum += 25 * 30;
      totalDecodeTimeSeconds += 0.09;
      jitterBufferDelaySeconds += 30 * 0.08;
      jitterBufferTargetDelaySeconds += 30 * 0.1;
      jitterBufferEmittedCount += 30;
      if (phase === "constrained" || phase === "impaired") {
        nackCount += 1;
      }
      packetsReceived += 100;
      if (phase === "impaired") {
        retransmittedPacketsReceived += 1;
        pacerSentRTX += 1;
      }
      samples.push({
        bytesReceived,
        currentRoundTripTimeSeconds: 0.08,
        elapsedMilliseconds,
        encoderTargetKbps,
        frameHeight: 1080,
        frameWidth: 1920,
        freezeCount: 0,
        framesDecoded,
        iceConnectionState: "connected",
        jitterBufferDelaySeconds,
        jitterBufferEmittedCount,
        jitterBufferTargetDelaySeconds,
        lossAverage: phase === "impaired" ? 0.02 : 0,
        localCandidateType: "relay",
        localCandidatePort: elapsedMilliseconds < 50_000 ? 50_000 : 50_001,
        localCandidateProtocol: "udp",
        nackCount,
        packetsReceived,
        peerConnectionState: "connected",
        pacerSentRTX,
        pacerTargetBitrateKbps: encoderTargetKbps,
        phase,
        playback: "Playing",
        remoteCandidateType: "relay",
        remoteCandidatePort: elapsedMilliseconds < 50_000 ? 60_000 : 60_001,
        remoteCandidateProtocol: "udp",
        retransmittedPacketsReceived,
        qpSum,
        totalDecodeTimeSeconds,
        totalFreezesDurationSeconds: 0,
        twccTargetKbps: encoderTargetKbps,
      });
    }
  }
  const manifest = {
    generatedAt: "2026-08-13T00:00:00Z",
    git: { dirty: false, revision: "deadbeef" },
    networkImpairment: { scope: "selected relay media flow only" },
    video: {
      codec: "H264",
      framesPerSecond: 30,
      height: 1080,
      width: 1920,
      playoutDelayHintSeconds: 0.1,
    },
    webrtc: { rtxNegotiated: true },
    phases: phases.map(([name, , , capacityKbps]) => ({
      name,
      shaping: capacityKbps > 0 ? { capacityKbps } : null,
    })),
  };
  const result = analyze(samples, manifest, {
    constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
    impaired: [
      {
        kind: "netem",
        drops: 40,
        packets: 3_000,
        options: { "loss-random": { loss: 0.02 } },
      },
    ],
  });
  assert.equal(result.passed, true, JSON.stringify(result.assertions, null, 2));
  const chart = renderSVG(result, manifest);
  assert.match(chart, /Adaptive sender response to controlled link changes/);
  assert.match(chart, /Encoder media/);
  assert.match(chart, /TWCC media/);
  assert.match(chart, /Received media/);
  assert.match(chart, /Pacer wire/);
  assert.match(chart, /Link capacity/);
  assert.match(chart, /y="548"[^>]*>Media rates/);
  assert.match(chart, /stroke="#be185d"[^>]+points="[^"]+"/);
  assert.match(chart, /stroke="#7c3aed"[^>]+points="[^"]+"/);
  const networkConditions = renderNetworkConditionsSVG(result, manifest);
  assert.match(networkConditions, /Controlled network conditions/);
  assert.match(networkConditions, /Injected random loss/);
  const playbackQuality = renderPlaybackQualitySVG(result, manifest);
  assert.match(playbackQuality, /Decoded frame rate/);
  assert.match(playbackQuality, /Freeze duration/);
  assert.match(playbackQuality, /H\.264 QP/);
  assert.match(playbackQuality, />35 fps</);
  assert.match(playbackQuality, />0\.1 s</);
  assert.match(playbackQuality, />51 QP</);
  const transportEvidence = renderTransportEvidenceSVG(result, manifest);
  assert.match(transportEvidence, /NACK \/ RTX/);
  assert.match(transportEvidence, /FlexFEC received/);
  assert.match(transportEvidence, /Injected \/ TWCC loss/);
  assert.match(transportEvidence, /300 ms phase-average buffer limit/);
  assert.match(transportEvidence, />[0-9]+ packets</);
  assert.match(transportEvidence, />5\.0 %</);
  assert.ok(
    (transportEvidence.match(/stroke="#059669"/g) || []).length > 6,
    "phase-boundary counter gaps must render as separate line segments",
  );
  const report = renderMarkdown(result, {
    ...manifest,
    networkImpairment: { scope: "producer-turn-transport" },
  });
  assert.match(
    report,
    /Media, TURN permissions, and TURN channel traffic share/,
  );
  assert.match(report, /## Qualification decision/);
  assert.match(report, /\| Shaper activation \| not measured \|/);
  assert.match(
    report,
    /\| Packet repair \| NACK [0-9]+; sender RTX [0-9]+; receiver RTX [0-9]+ \|/,
  );
  assert.equal(result.trafficControl.impairedDropRatio, 40 / 2040);
  assert.equal(result.candidatePairSwitches, 1);
  const ceilingResult = analyze(samples, {
    ...manifest,
    video: {
      ...manifest.video,
      adaptive: {
        initialBitrateKbps: 4000,
        minimumBitrateKbps: 2000,
        maximumBitrateKbps: 5500,
        changeThresholdPct: 10,
        maxIncreaseLossPct: 1,
      },
    },
  });
  assert.equal(
    ceilingResult.assertions.find(
      (assertion) => assertion.name === "healthy-link-quality-ceiling",
    ).passed,
    true,
  );
  assert.equal(ceilingResult.healthyLinkTargetRatio, 1);
  const cappedResult = analyze(samples, {
    ...manifest,
    video: {
      ...manifest.video,
      adaptive: {
        initialBitrateKbps: 4000,
        minimumBitrateKbps: 2000,
        maximumBitrateKbps: 6000,
        changeThresholdPct: 10,
        maxIncreaseLossPct: 1,
      },
    },
  });
  assert.equal(
    cappedResult.assertions.find(
      (assertion) => assertion.name === "healthy-link-quality-ceiling",
    ).passed,
    false,
  );
  assert.equal(cappedResult.healthyLinkTargetRatio, 0);
  const pathLimitedRelayResult = analyze(samples, {
    ...manifest,
    networkPath: { icePolicy: "relay", kind: "relay" },
    video: {
      ...manifest.video,
      adaptive: {
        initialBitrateKbps: 4000,
        minimumBitrateKbps: 2000,
        maximumBitrateKbps: 8000,
        changeThresholdPct: 10,
        maxIncreaseLossPct: 1,
      },
    },
  });
  assert.equal(
    pathLimitedRelayResult.assertions.some(
      (assertion) => assertion.name === "healthy-link-quality-ceiling",
    ),
    false,
  );
  assert.equal(result.phases.impaired.averageQP, 25);
  assert.ok(
    Math.abs(result.phases.impaired.averageDecodeMilliseconds - 3) < 0.0001,
  );
  assert.ok(
    Math.abs(result.phases.impaired.averageJitterBufferDelayMilliseconds - 80) <
      0.0001,
  );
  assert.ok(
    Math.abs(
      result.phases.impaired.averageJitterBufferTargetMilliseconds - 100,
    ) < 0.0001,
  );
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "playout-target-latency-budget",
    ).passed,
    true,
  );
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "playout-effective-latency-budget",
    ).passed,
    true,
  );

  const excessivePlayout = analyze(
    samples.map((sample) => ({
      ...sample,
      jitterBufferTargetDelaySeconds:
        (sample.jitterBufferEmittedCount || 0) * 0.251,
    })),
    manifest,
  );
  assert.equal(
    excessivePlayout.assertions.find(
      (assertion) => assertion.name === "playout-target-latency-budget",
    ).passed,
    false,
  );

  const excessiveEffectivePlayout = analyze(
    samples.map((sample) => ({
      ...sample,
      jitterBufferDelaySeconds: (sample.jitterBufferEmittedCount || 0) * 0.301,
    })),
    manifest,
  );
  assert.equal(
    excessiveEffectivePlayout.assertions.find(
      (assertion) => assertion.name === "playout-target-latency-budget",
    ).passed,
    true,
  );
  assert.equal(
    excessiveEffectivePlayout.assertions.find(
      (assertion) => assertion.name === "playout-effective-latency-budget",
    ).passed,
    false,
  );

  const stableAdditionalPressure = analyze(
    samples.map((sample) => ({
      ...sample,
      encoderTargetKbps:
        sample.phase === "constrained"
          ? 2100
          : sample.phase === "impaired"
            ? 2300
            : sample.encoderTargetKbps,
    })),
    manifest,
  );
  assert.equal(
    stableAdditionalPressure.assertions.find(
      (assertion) => assertion.name === "continued-pressure",
    ).passed,
    true,
  );
  const increasingAdditionalPressure = analyze(
    samples.map((sample, index) => ({
      ...sample,
      encoderTargetKbps:
        sample.phase === "impaired"
          ? index % 20 >= 10
            ? 2500
            : 2300
          : sample.encoderTargetKbps,
    })),
    manifest,
  );
  assert.equal(
    increasingAdditionalPressure.assertions.find(
      (assertion) => assertion.name === "continued-pressure",
    ).passed,
    false,
  );

  const underdeliveredTarget = analyze(
    samples.map((sample) => ({
      ...sample,
      encoderTargetKbps:
        sample.phase === "impaired" ? 3000 : sample.encoderTargetKbps,
    })),
    manifest,
  );
  assert.equal(
    underdeliveredTarget.assertions.find(
      (assertion) => assertion.name === "impaired-target-efficiency",
    ).passed,
    false,
  );

  let reconstructedBytes = 0;
  const recoveryFromLoss = samples.map((sample) => {
    const receivedKbps = {
      warmup: 4200,
      baseline: 4000,
      constrained: 3000,
      impaired: 1500,
      recovery: 2000,
    }[sample.phase];
    reconstructedBytes += (receivedKbps * 1000) / 8;
    return { ...sample, bytesReceived: reconstructedBytes };
  });
  const recoveryFromLossResult = analyze(recoveryFromLoss, manifest);
  assert.equal(
    recoveryFromLossResult.assertions.find(
      (assertion) => assertion.name === "throughput-recovery",
    ).passed,
    false,
  );
  let recoveredBytes = 0;
  const recoveredStream = samples.map((sample) => {
    const receivedKbps = {
      warmup: 4200,
      baseline: 4000,
      constrained: 3000,
      impaired: 1500,
      recovery: 3000,
    }[sample.phase];
    recoveredBytes += (receivedKbps * 1000) / 8;
    return { ...sample, bytesReceived: recoveredBytes };
  });
  const recoveredStreamResult = analyze(recoveredStream, manifest);
  assert.equal(
    recoveredStreamResult.assertions.find(
      (assertion) => assertion.name === "throughput-recovery",
    ).passed,
    true,
  );
  const incompleteRateRecovery = analyze(
    samples.map((sample) => ({
      ...sample,
      encoderTargetKbps:
        sample.phase === "recovery" ? 3000 : sample.encoderTargetKbps,
    })),
    manifest,
  );
  assert.equal(
    incompleteRateRecovery.assertions.find(
      (assertion) => assertion.name === "sustained-recovery",
    ).passed,
    false,
  );
  let lateRecoveryIndex = 0;
  const lateNetworkDegradation = analyze(
    samples.map((sample) => {
      if (sample.phase !== "recovery") {
        return sample;
      }
      lateRecoveryIndex += 1;
      return {
        ...sample,
        encoderTargetKbps: lateRecoveryIndex <= 12 ? 4000 : 3000,
      };
    }),
    manifest,
  );
  assert.equal(
    lateNetworkDegradation.assertions.find(
      (assertion) => assertion.name === "sustained-recovery",
    ).passed,
    true,
  );
  let transientRecoveryIndex = 0;
  const transientRecoverySpike = analyze(
    samples.map((sample) => {
      if (sample.phase !== "recovery") {
        return sample;
      }
      transientRecoveryIndex += 1;
      return {
        ...sample,
        encoderTargetKbps: transientRecoveryIndex === 10 ? 4000 : 3000,
      };
    }),
    manifest,
  );
  assert.equal(
    transientRecoverySpike.assertions.find(
      (assertion) => assertion.name === "sustained-recovery",
    ).passed,
    false,
  );
  let boundedRecoveryCorrectionIndex = 0;
  const boundedRecoveryCorrection = analyze(
    samples.map((sample) => {
      if (sample.phase !== "recovery") {
        return sample;
      }
      boundedRecoveryCorrectionIndex += 1;
      return {
        ...sample,
        encoderTargetKbps:
          boundedRecoveryCorrectionIndex === 8 ||
          boundedRecoveryCorrectionIndex === 9
            ? 3000
            : 4000,
      };
    }),
    manifest,
  );
  assert.equal(
    boundedRecoveryCorrection.assertions.find(
      (assertion) => assertion.name === "sustained-recovery",
    ).passed,
    true,
  );

  const receiverSamples = samples.map((sample, index) => ({
    datagramsReceived: index * 100,
    datagramsSent: index * 10,
    inputErrors: 0,
    noPortDrops: 0,
    phase: sample.phase,
    receiveBufferDrops: 0,
    sendBufferDrops: 0,
    source: "linux-network-namespace",
  }));
  const receiverObserved = analyze(
    samples,
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
    null,
    receiverSamples,
  );
  assert.equal(
    receiverObserved.assertions.find(
      (assertion) => assertion.name === "receiver-kernel-capacity",
    ).passed,
    true,
  );
  assert.match(
    renderMarkdown(receiverObserved, manifest),
    /Receiver-kernel UDP diagnostics/,
  );
  const producerSamples = samples.map((sample, index) => ({
    datagramsReceived: index * 10,
    datagramsSent: index * 100,
    inputErrors: 0,
    noPortDrops: 0,
    phase: sample.phase,
    receiveBufferDrops: 0,
    sendBufferDrops: 0,
    source: "linux-docker-network-namespace",
  }));
  const bothKernelsObserved = analyze(
    samples,
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
    null,
    receiverSamples,
    producerSamples,
  );
  assert.equal(
    bothKernelsObserved.assertions.find(
      (assertion) => assertion.name === "producer-kernel-capacity",
    ).passed,
    true,
  );
  assert.match(
    renderMarkdown(bothKernelsObserved, manifest),
    /Producer-kernel UDP diagnostics/,
  );
  const producerOverflow = analyze(
    samples,
    manifest,
    null,
    null,
    receiverSamples,
    producerSamples.map((sample, index) => ({
      ...sample,
      sendBufferDrops: index >= 70 ? 1 : 0,
    })),
  );
  assert.equal(
    producerOverflow.assertions.find(
      (assertion) => assertion.name === "producer-kernel-capacity",
    ).passed,
    false,
  );
  const attributableProducerLoss = analyze(
    samples,
    manifest,
    {
      constrained: {
        start: [{ kind: "netem", drops: 0, packets: 0 }],
        end: [{ kind: "netem", drops: 5, packets: 1_000 }],
      },
      impaired: {
        start: [{ kind: "netem", drops: 10, packets: 1_000 }],
        end: [
          {
            kind: "netem",
            drops: 50,
            packets: 2_000,
            options: { "loss-random": { loss: 0.02 } },
          },
        ],
      },
    },
    null,
    receiverSamples,
    producerSamples.map((sample, index) => ({
      ...sample,
      sendBufferDrops: index >= 60 ? 25 : index >= 40 ? 5 : 0,
    })),
  );
  assert.equal(
    attributableProducerLoss.assertions.find(
      (assertion) => assertion.name === "producer-kernel-capacity",
    ).passed,
    true,
  );
  const producerLossOutsideBoundaryTolerance = analyze(
    samples,
    manifest,
    {
      constrained: {
        start: [{ kind: "netem", drops: 0, packets: 0 }],
        end: [{ kind: "netem", drops: 5, packets: 1_000 }],
      },
      impaired: {
        start: [{ kind: "netem", drops: 10, packets: 1_000 }],
        end: [
          {
            kind: "netem",
            drops: 50,
            packets: 2_000,
            options: { "loss-random": { loss: 0.02 } },
          },
        ],
      },
    },
    null,
    receiverSamples,
    producerSamples.map((sample, index) => ({
      ...sample,
      sendBufferDrops: index >= 60 ? 28 : index >= 40 ? 8 : 0,
    })),
  );
  assert.equal(
    producerLossOutsideBoundaryTolerance.assertions.find(
      (assertion) => assertion.name === "producer-kernel-capacity",
    ).passed,
    false,
  );
  const receiverOverflow = analyze(
    samples,
    manifest,
    null,
    null,
    receiverSamples.map((sample, index) => ({
      ...sample,
      receiveBufferDrops: index >= 70 ? 1 : 0,
    })),
  );
  assert.equal(
    receiverOverflow.assertions.find(
      (assertion) => assertion.name === "receiver-kernel-capacity",
    ).passed,
    false,
  );

  const resetQdiscCounters = analyze(
    samples,
    manifest,
    {
      constrainedTransitions: [
        {
          end: [{ kind: "netem", drops: 0, packets: 500 }],
          start: [{ kind: "netem", drops: 0, packets: 0 }],
        },
      ],
      constrained: {
        end: [{ kind: "netem", drops: 10, packets: 1_000 }],
        start: [{ kind: "netem", drops: 10, packets: 500 }],
      },
      impaired: {
        end: [
          {
            kind: "netem",
            drops: 40,
            packets: 1_960,
            options: { "loss-random": { loss: 0.02 } },
          },
        ],
        start: [{ kind: "netem", drops: 100, packets: 5_000 }],
      },
    },
    Object.fromEntries(
      phases.map(([name]) => [
        name,
        { averageQP: 25, encodedBytes: 100_000, frames: 600 },
      ]),
    ),
  );
  assert.equal(
    resetQdiscCounters.passed,
    true,
    JSON.stringify(resetQdiscCounters.assertions),
  );
  assert.equal(resetQdiscCounters.trafficControl.impairedDropRatio, 0.02);

  const incompleteRecoveryDrain = analyze(samples, manifest, {
    constrained: {
      start: [{ kind: "netem", drops: 0, packets: 0 }],
      end: [{ kind: "netem", drops: 0, packets: 1_000 }],
    },
    impaired: {
      start: [{ kind: "netem", drops: 0, packets: 1_000 }],
      end: [
        {
          kind: "netem",
          drops: 20,
          packets: 2_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
    recoveryDrain: {
      start: [{ kind: "netem", drops: 20, packets: 2_000, qlen: 100 }],
      end: [
        {
          kind: "netem",
          drops: 20,
          packets: 3_000,
          qlen: 32,
          options: { limit: 256 },
        },
      ],
    },
  });
  assert.equal(
    incompleteRecoveryDrain.assertions.find(
      (assertion) => assertion.name === "traffic-control-recovery-drain",
    ).passed,
    false,
  );
  const boundedActiveRecoveryQueue = analyze(samples, manifest, {
    constrained: {
      start: [{ kind: "netem", drops: 0, packets: 0 }],
      end: [{ kind: "netem", drops: 0, packets: 1_000 }],
    },
    impaired: {
      start: [{ kind: "netem", drops: 0, packets: 1_000 }],
      end: [
        {
          kind: "netem",
          drops: 20,
          packets: 2_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
    recoveryDrain: {
      start: [{ kind: "netem", drops: 20, packets: 2_000, qlen: 8 }],
      end: [
        {
          kind: "netem",
          drops: 20,
          packets: 3_000,
          qlen: 8,
          options: { limit: 256 },
        },
      ],
    },
  });
  assert.equal(
    boundedActiveRecoveryQueue.assertions.find(
      (assertion) => assertion.name === "traffic-control-recovery-drain",
    ).passed,
    true,
  );

  const boundedWithAdmissionDrops = analyze(
    samples.map((sample, index) => ({
      ...sample,
      pacerMaximumQueueDelayMilliseconds: 250,
      pacerMediaFramesDropped: index,
      pacerQueueDrops: 0,
      recoveryKeyFrameFailures: 0,
      recoveryKeyFrameRequests: index > 0 ? 1 : 0,
    })),
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(
    boundedWithAdmissionDrops.assertions.find(
      (assertion) => assertion.name === "bounded-pacer-capacity",
    ).passed,
    true,
  );
  assert.equal(
    boundedWithAdmissionDrops.assertions.find(
      (assertion) => assertion.name === "pacer-recovery-keyframes",
    ).passed,
    true,
  );
  const observedRTCPRecovery = analyze(
    samples.map((sample, index) => ({
      ...sample,
      pliCount: index > 50 ? 1 : 0,
      recoveryKeyFrameFailures: 0,
      recoveryKeyFrameRequests: index > 50 ? 1 : 0,
      rtcpKeyFrameRequests: index > 50 ? 1 : 0,
    })),
    manifest,
  );
  assert.equal(
    observedRTCPRecovery.assertions.find(
      (assertion) => assertion.name === "rtcp-keyframe-feedback",
    ).passed,
    true,
  );
  const discardedRTCPRecovery = analyze(
    samples.map((sample, index) => ({
      ...sample,
      pliCount: index > 50 ? 1 : 0,
      recoveryKeyFrameFailures: 0,
      recoveryKeyFrameRequests: 0,
      rtcpKeyFrameRequests: 0,
    })),
    manifest,
  );
  assert.equal(
    discardedRTCPRecovery.assertions.find(
      (assertion) => assertion.name === "rtcp-keyframe-feedback",
    ).passed,
    false,
  );
  const malformedRTCPRecovery = analyze(
    samples.map((sample, index) => ({
      ...sample,
      recoveryKeyFrameFailures: 0,
      recoveryKeyFrameRequests: 0,
      rtcpMalformedFeedback: index > 50 ? 1 : 0,
    })),
    manifest,
  );
  assert.equal(
    malformedRTCPRecovery.assertions.find(
      (assertion) => assertion.name === "rtcp-feedback-integrity",
    ).passed,
    false,
  );
  const failedAdaptiveReconfiguration = analyze(
    samples.map((sample, index) => ({
      ...sample,
      adaptiveBitrateFailures: index > 50 ? 1 : 0,
      adaptiveBitrateUpdates: index > 20 ? 1 : 0,
    })),
    manifest,
  );
  assert.equal(
    failedAdaptiveReconfiguration.assertions.find(
      (assertion) => assertion.name === "adaptive-reconfiguration-integrity",
    ).passed,
    false,
  );

  for (const [name, recoveryKeyFrameRequests, recoveryKeyFrameFailures] of [
    ["missing request", 0, 0],
    ["rejected request", 1, 1],
  ]) {
    const brokenRecovery = analyze(
      samples.map((sample, index) => ({
        ...sample,
        pacerMediaFramesDropped: index,
        recoveryKeyFrameFailures,
        recoveryKeyFrameRequests,
      })),
      manifest,
    );
    assert.equal(
      brokenRecovery.assertions.find(
        (assertion) => assertion.name === "pacer-recovery-keyframes",
      ).passed,
      false,
      name,
    );
  }
  const unboundedQueue = analyze(
    samples.map((sample) => ({
      ...sample,
      pacerMaximumQueueDelayMilliseconds: 376,
      pacerQueueDrops: 0,
    })),
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(
    unboundedQueue.assertions.find(
      (assertion) => assertion.name === "bounded-pacer-capacity",
    ).passed,
    false,
  );
  const unboundedAdmissionQueue = analyze(
    samples.map((sample) => ({
      ...sample,
      pacerMaximumAdmittedDelayMilliseconds: 226,
      pacerMaximumQueueDelayMilliseconds: 250,
      pacerQueueDrops: 0,
    })),
    manifest,
  );
  assert.equal(
    unboundedAdmissionQueue.assertions.find(
      (assertion) => assertion.name === "bounded-pacer-capacity",
    ).passed,
    false,
  );

  const brokenTWCC = analyze(
    samples.map((sample, index) => ({
      ...sample,
      twccFeedbackPackets: index + 1,
      twccMalformedFeedback: index === samples.length - 1 ? 1 : 0,
      twccReportedLost: index * 50,
      twccReportedStatuses: index * 100,
    })),
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(
    brokenTWCC.assertions.find(
      (assertion) => assertion.name === "twcc-feedback-integrity",
    ).passed,
    false,
  );
  const highLossWithoutGuardResponse = analyze(
    samples.map((sample, index) => ({
      ...sample,
      lossGuardReductions: 0,
      twccReportedLost: index * 20,
      twccReportedStatuses: index * 100,
    })),
    manifest,
  );
  assert.equal(
    highLossWithoutGuardResponse.assertions.find(
      (assertion) => assertion.name === "loss-guard-response",
    ).passed,
    false,
  );
  const highLossWithGuardResponse = analyze(
    samples.map((sample, index) => ({
      ...sample,
      lossGuardReductions: index === 0 ? 0 : 1,
      twccReportedLost: index * 20,
      twccReportedStatuses: index * 100,
    })),
    manifest,
  );
  assert.equal(
    highLossWithGuardResponse.assertions.find(
      (assertion) => assertion.name === "loss-guard-response",
    ).passed,
    true,
  );
  assert.equal(
    brokenTWCC.assertions.find(
      (assertion) => assertion.name === "twcc-loss-fidelity",
    ).passed,
    false,
  );

  let degradedQPSum = 0;
  const degradedQuality = analyze(
    samples.map((sample) => {
      degradedQPSum += (sample.phase === "impaired" ? 50 : 25) * 30;
      return {
        ...sample,
        frameWidth: sample.phase === "impaired" ? 1280 : 1920,
        qpSum: degradedQPSum,
      };
    }),
    manifest,
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(
    degradedQuality.assertions.find(
      (assertion) => assertion.name === "impaired-compression-quality",
    ).passed,
    false,
  );
  assert.equal(
    degradedQuality.assertions.find(
      (assertion) => assertion.name === "decoded-resolution",
    ).passed,
    false,
  );

  let fecPacketsReceived = 0;
  let pacerSentFEC = 0;
  const fecSamples = samples.map((sample) => {
    if (sample.phase === "impaired") {
      fecPacketsReceived += 1;
      pacerSentFEC += 1;
    }
    return {
      ...sample,
      fecPacketsReceived,
      flexFECMediaPackets: 5,
      flexFECRepairPackets: 1,
      pacerSentFEC,
      pacerTargetBitrateKbps: sample.twccTargetKbps * 1.2,
      pacerPacingBitrateKbps: sample.twccTargetKbps * 1.8,
    };
  });
  const fecResult = analyze(
    fecSamples,
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(fecResult.passed, true, JSON.stringify(fecResult.assertions));
  assert.equal(
    fecResult.assertions.find(
      (assertion) => assertion.name === "flexfec-sender-pacing",
    ).passed,
    true,
  );
  const missingSenderFEC = analyze(
    fecSamples.map((sample) => ({ ...sample, pacerSentFEC: 0 })),
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
  );
  assert.equal(
    missingSenderFEC.assertions.find(
      (assertion) => assertion.name === "flexfec-sender-pacing",
    ).passed,
    false,
  );
  const collapsedPacingHeadroom = analyze(
    fecSamples.map((sample) => ({
      ...sample,
      pacerPacingBitrateKbps: sample.twccTargetKbps * 1.5,
    })),
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
  );
  assert.equal(
    collapsedPacingHeadroom.assertions.find(
      (assertion) => assertion.name === "flexfec-burst-headroom",
    ).passed,
    false,
  );
  const proactiveRepairWinsTheRace = analyze(
    fecSamples.map((sample) => ({
      ...sample,
      retransmittedPacketsReceived: 0,
    })),
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
  );
  assert.equal(
    proactiveRepairWinsTheRace.assertions.some(
      (assertion) => assertion.name === "rtx-repair",
    ),
    false,
  );
  assert.equal(
    proactiveRepairWinsTheRace.assertions.find(
      (assertion) => assertion.name === "rtx-sender-pacing",
    ).passed,
    true,
  );
  const mismatchedFEC = analyze(
    fecSamples.map((sample) => ({
      ...sample,
      flexFECRepairPackets: 2,
    })),
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
  );
  assert.equal(
    mismatchedFEC.assertions.find(
      (assertion) => assertion.name === "flexfec-configuration",
    ).passed,
    false,
  );

  const fittingSamples = fecSamples.map((sample) => ({
    ...sample,
    encoderTargetKbps:
      sample.phase === "baseline" || sample.phase === "warmup" ? 1800 : 1600,
  }));
  const fittingResult = analyze(
    fittingSamples,
    {
      ...manifest,
      protection: {
        flexFEC: true,
        flexFECMediaPackets: 5,
        flexFECRepairPackets: 1,
      },
      webrtc: { flexFECNegotiated: true, rtxNegotiated: true },
    },
    {
      constrained: [{ kind: "netem", drops: 0, packets: 1_000 }],
      impaired: [
        {
          kind: "netem",
          drops: 40,
          packets: 3_000,
          options: { "loss-random": { loss: 0.02 } },
        },
      ],
    },
  );
  assert.equal(fittingResult.congestionResponseRequired, false);
  for (const name of [
    "congestion-response",
    "response-time",
    "recovery-time",
    "throughput-recovery",
  ]) {
    assert.equal(
      fittingResult.assertions.find((assertion) => assertion.name === name)
        .passed,
      true,
      name,
    );
  }

  const invalidProfile = analyze(samples, manifest, {
    constrained: [
      {
        kind: "netem",
        drops: 100,
        packets: 1_000,
        options: { "loss-random": { loss: 0.01 } },
      },
    ],
    impaired: [
      {
        kind: "netem",
        drops: 300,
        packets: 3_000,
        options: { "loss-random": { loss: 0.03 } },
      },
    ],
  });
  assert.equal(
    invalidProfile.assertions.find(
      (assertion) => assertion.name === "capacity-profile-configuration",
    ).passed,
    false,
  );
  assert.equal(
    invalidProfile.assertions.find(
      (assertion) => assertion.name === "loss-profile-configuration",
    ).passed,
    false,
  );
  assert.equal(
    invalidProfile.assertions.find(
      (assertion) => assertion.name === "traffic-control-drop-budget",
    ).passed,
    false,
  );

  const saturatedQdisc = analyze(samples, manifest, {
    constrained: {
      start: [{ kind: "netem", drops: 0, packets: 0 }],
      end: [
        {
          kind: "netem",
          drops: 0,
          options: { limit: 256 },
          packets: 1_000,
          qlen: 200,
        },
      ],
    },
    impaired: {
      start: [{ kind: "netem", drops: 0, packets: 1_000 }],
      end: [
        {
          kind: "netem",
          drops: 40,
          options: { limit: 256, "loss-random": { loss: 0.02 } },
          packets: 3_000,
          qlen: 240,
        },
      ],
    },
  });
  assert.equal(
    saturatedQdisc.assertions.find(
      (assertion) => assertion.name === "traffic-control-queue-headroom",
    ).passed,
    false,
  );
});

test("rejects a stream that never adapts to congestion", () => {
  const phases = [
    ["warmup", 0],
    ["baseline", 0],
    ["constrained", 2500],
    ["impaired", 1800],
    ["recovery", 0],
  ];
  const phaseNames = phases.map(([name]) => name);
  const samples = [];
  let bytesReceived = 0;
  let elapsedMilliseconds = 0;
  for (const phase of phaseNames) {
    for (let index = 0; index < 20; index += 1) {
      elapsedMilliseconds += 1000;
      bytesReceived += 500_000;
      samples.push({
        bytesReceived,
        elapsedMilliseconds,
        encoderTargetKbps: 5000,
        freezeCount: 0,
        framesDecoded: index + 1,
        iceConnectionState: "connected",
        localCandidateType: "relay",
        nackCount: phase === "impaired" ? index + 1 : 0,
        peerConnectionState: "connected",
        phase,
        playback: "Playing",
        remoteCandidateType: "relay",
        totalFreezesDurationSeconds: 0,
        twccTargetKbps: 5000,
      });
    }
  }
  const result = analyze(samples, {
    phases: phases.map(([name, capacityKbps]) => ({
      name,
      shaping: capacityKbps > 0 ? { capacityKbps } : null,
    })),
  });
  assert.equal(result.passed, false);
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "congestion-response",
    ).passed,
    false,
  );
});

test("rejects negotiated RTX when loss produces no repair packets", () => {
  const phases = [
    ["warmup", 5000, 4200, 0],
    ["baseline", 5000, 4000, 0],
    ["constrained", 2100, 2000, 2500],
    ["impaired", 1800, 1600, 2500],
    ["recovery", 4000, 3500, 0],
  ];
  const samples = [];
  let elapsedMilliseconds = 0;
  let bytesReceived = 0;
  let framesDecoded = 0;
  let nackCount = 0;
  let packetsReceived = 0;
  for (const [phase, encoderTargetKbps, receivedBitrateKbps] of phases) {
    for (let index = 0; index < 20; index += 1) {
      elapsedMilliseconds += 1000;
      bytesReceived += (receivedBitrateKbps * 1000) / 8;
      framesDecoded += 30;
      packetsReceived += 100;
      if (phase === "constrained" || phase === "impaired") {
        nackCount += 1;
      }
      samples.push({
        bytesReceived,
        currentRoundTripTimeSeconds: 0.08,
        elapsedMilliseconds,
        encoderTargetKbps,
        framesDecoded,
        iceConnectionState: "connected",
        localCandidateType: "relay",
        nackCount,
        packetsReceived,
        peerConnectionState: "connected",
        phase,
        playback: "Playing",
        remoteCandidateType: "relay",
        retransmittedPacketsReceived: 0,
        totalFreezesDurationSeconds: 0,
        twccTargetKbps: encoderTargetKbps,
      });
    }
  }
  const result = analyze(samples, {
    phases: phases.map(([name, , , capacityKbps]) => ({
      name,
      shaping: capacityKbps > 0 ? { capacityKbps } : null,
    })),
    webrtc: { rtxNegotiated: true },
  });
  assert.equal(result.passed, false);
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "rtx-sender-pacing",
    ).passed,
    false,
  );
  assert.equal(
    result.assertions.find((assertion) => assertion.name === "rtx-repair")
      .passed,
    false,
  );
});

test("rejects a connected stream that freezes under impairment", () => {
  const phases = [
    ["warmup", 5000, 4200, 0],
    ["baseline", 5000, 4000, 0],
    ["constrained", 2000, 1700, 2500],
    ["impaired", 1800, 1600, 2500],
    ["recovery", 4000, 3500, 0],
  ];
  const samples = [];
  let bytesReceived = 0;
  let elapsedMilliseconds = 0;
  let framesDecoded = 0;
  let freezeDuration = 0;
  let nackCount = 0;
  for (const [phase, encoderTargetKbps, receivedBitrateKbps] of phases) {
    for (let index = 0; index < 20; index += 1) {
      elapsedMilliseconds += 1000;
      bytesReceived += (receivedBitrateKbps * 1000) / 8;
      if (phase === "impaired" && index % 2 === 0) {
        freezeDuration += 1;
      } else {
        framesDecoded += 30;
      }
      if (phase === "constrained" || phase === "impaired") {
        nackCount += 1;
      }
      samples.push({
        bytesReceived,
        elapsedMilliseconds,
        encoderTargetKbps,
        framesDecoded,
        iceConnectionState: "connected",
        localCandidateType: "relay",
        nackCount,
        peerConnectionState: "connected",
        phase,
        playback: "Playing",
        remoteCandidateType: "relay",
        totalFreezesDurationSeconds: freezeDuration,
        twccTargetKbps: encoderTargetKbps,
      });
    }
  }
  const result = analyze(samples, {
    phases: phases.map(([name, , , capacityKbps]) => ({
      name,
      shaping: capacityKbps > 0 ? { capacityKbps } : null,
    })),
  });
  assert.equal(result.passed, false);
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "impaired-link-freezes",
    ).passed,
    false,
  );
  assert.equal(
    result.assertions.find(
      (assertion) => assertion.name === "decoded-frame-rate",
    ).passed,
    false,
  );
});
