import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { join } from "node:path";
import { parseQualityState } from "../../../../shared/quality-client.ts";

const rate = (profile) =>
  profile.frameRate.numerator / profile.frameRate.denominator;
const observed = (state) => {
  const format = state?.sourceFormat;
  if (!format || format.unconfirmedEncoders || format.activeEncoders !== 1)
    return null;
  return (
    format.profiles.find((profile) => profile.observedEncoders === 1) ?? null
  );
};
const definition = (format) =>
  JSON.stringify({
    default: format.defaultProfile,
    profiles: format.profiles.map(({ id, width, height, frameRate }) => ({
      id,
      width,
      height,
      frameRate,
    })),
  });

// Observe the existing API and decoded output independently of the format
// worker's decision. Counts describing a request never prove a format applied.
export function summarizeSourceFormats(
  samples,
  networkProfile,
  minimumDwellMilliseconds = 10000,
) {
  if (
    !/^[a-z][a-z0-9-]{0,31}$/.test(networkProfile) ||
    !Number.isFinite(minimumDwellMilliseconds) ||
    minimumDwellMilliseconds < 0
  )
    throw new Error("invalid automatic-format expectations");
  const relevant = samples.filter((sample) =>
    ["baseline", "source-network", "viewer-network", "recovery"].includes(
      sample.phase,
    ),
  );
  const failures = [];
  const states = relevant.map((sample) => {
    try {
      const state = parseQualityState(sample.sourceQuality);
      if (
        state.selected !== "auto" ||
        state.activeEncoders !== 1 ||
        state.failedUpdates !== 0 ||
        !state.sourceFormat?.adaptive ||
        state.sourceFormat.activeEncoders !== 1 ||
        state.sourceFormat.failedUpdates !== 0
      )
        throw new Error();
      return state;
    } catch {
      failures.push(
        "source quality is missing, invalid, not automatic or reports an update failure",
      );
      return null;
    }
  });
  const initial = states.find(Boolean)?.sourceFormat;
  const profiles = initial?.profiles ?? [];
  const reference = profiles.find(
    (profile) => profile.id === initial?.defaultProfile,
  );
  const expectedNetwork = profiles.find(
    (profile) => profile.id === networkProfile,
  );
  const gates = {
    qualityComplete: relevant.length > 0 && failures.length === 0,
    stableConfiguration:
      Boolean(initial) &&
      states.every(
        (state) =>
          state && definition(state.sourceFormat) === definition(initial),
      ),
    lowerNetworkFormat: Boolean(
      reference &&
      expectedNetwork &&
      expectedNetwork.width * expectedNetwork.height <
        reference.width * reference.height &&
      rate(expectedNetwork) <= rate(reference),
    ),
    samplingContinuous: relevant.length > 1,
    sampledMediaContinuous: relevant.length > 1,
    decodedCountersContinuous: relevant.length > 1,
    singleSession:
      relevant.length > 0 &&
      relevant.every(
        (sample) =>
          sample.peerConnectionsCreated === 1 &&
          sample.whepSessionCreates === 1 &&
          sample.whepRestartPatches === 0,
      ),
    minimumDwell: true,
    expectedSteadyFormats: true,
    configuredCadence: true,
  };
  let maximumSampleGapMilliseconds = 0;
  const transitions = [];
  for (let index = 0; index < relevant.length; index++) {
    const sample = relevant[index];
    const previous = relevant[index - 1];
    if (
      !Number.isFinite(sample.elapsedMilliseconds) ||
      !Number.isFinite(sample.videoCurrentTimeSeconds)
    ) {
      gates.samplingContinuous = false;
      gates.sampledMediaContinuous = false;
    }
    if (!Number.isSafeInteger(sample.framesDecoded) || sample.framesDecoded < 0)
      gates.decodedCountersContinuous = false;
    if (previous) {
      const gap = sample.elapsedMilliseconds - previous.elapsedMilliseconds;
      maximumSampleGapMilliseconds = Math.max(
        maximumSampleGapMilliseconds,
        gap,
      );
      if (!(gap > 0 && gap <= 3000)) gates.samplingContinuous = false;
      if (sample.videoCurrentTimeSeconds < previous.videoCurrentTimeSeconds)
        gates.sampledMediaContinuous = false;
      if (sample.framesDecoded < previous.framesDecoded)
        gates.decodedCountersContinuous = false;
    }
    const profile = observed(states[index]);
    if (profile && transitions.at(-1)?.profile !== profile.id)
      transitions.push({
        profile: profile.id,
        atMilliseconds: sample.elapsedMilliseconds,
        phase: sample.phase,
      });
  }
  // An observation arrives at the next sample. Report and bound that timing
  // uncertainty rather than treating polling timestamps as exact native caps time.
  for (let index = 2; index < transitions.length; index++) {
    if (
      transitions[index].atMilliseconds -
        transitions[index - 1].atMilliseconds +
        maximumSampleGapMilliseconds <
      minimumDwellMilliseconds
    )
      gates.minimumDwell = false;
  }
  const networkPhases = ["source-network", "viewer-network"].filter((name) =>
    relevant.some((sample) => sample.phase === name),
  );
  if (networkPhases.length !== 1) gates.expectedSteadyFormats = false;
  const phaseNames = ["baseline", ...networkPhases, "recovery"];
  const observedPhases = relevant
    .filter((sample, index) => sample.phase !== relevant[index - 1]?.phase)
    .map((sample) => sample.phase);
  gates.phaseOrder =
    JSON.stringify(observedPhases) === JSON.stringify(phaseNames);
  const phases = {};
  for (const name of phaseNames) {
    const entries = relevant.flatMap((sample, index) =>
      sample.phase === name ? [{ sample, state: states[index] }] : [],
    );
    const expected =
      name === "baseline" || name === "recovery" ? reference : expectedNetwork;
    const tail = entries.filter(
      ({ sample }) =>
        sample.elapsedMilliseconds >=
        (entries.at(-1)?.sample.elapsedMilliseconds ?? 0) - 10000,
    );
    const duration =
      tail.length > 1
        ? tail.at(-1).sample.elapsedMilliseconds -
          tail[0].sample.elapsedMilliseconds
        : 0;
    const frames =
      tail.length > 1
        ? tail.at(-1).sample.framesDecoded - tail[0].sample.framesDecoded
        : 0;
    const decodedFPS = duration > 0 ? (frames * 1000) / duration : null;
    const steadyFormatPassed = Boolean(
      expected &&
      duration >= 8000 &&
      tail.length >= 4 &&
      tail.every(({ sample, state }) => {
        const profile = observed(state);
        return (
          profile?.id === expected.id &&
          state.sourceFormat.pendingEncoders === 0 &&
          sample.frameWidth === expected.width &&
          sample.frameHeight === expected.height
        );
      }),
    );
    let expectedFrames = 0;
    for (let index = 1; index < entries.length; index++) {
      const previous = entries[index - 1];
      const profile = observed(previous.state);
      if (!profile) {
        expectedFrames = NaN;
        break;
      }
      expectedFrames +=
        (rate(profile) *
          (entries[index].sample.elapsedMilliseconds -
            previous.sample.elapsedMilliseconds)) /
        1000;
    }
    const decodedFrames =
      entries.length > 1
        ? entries.at(-1).sample.framesDecoded - entries[0].sample.framesDecoded
        : null;
    const configuredFrameRatio =
      expectedFrames > 0 ? decodedFrames / expectedFrames : null;
    const cadencePassed = Boolean(
      expected &&
      Number.isFinite(decodedFPS) &&
      decodedFPS >= rate(expected) * 0.8 &&
      decodedFPS <= rate(expected) * 1.2 &&
      configuredFrameRatio >= 0.8,
    );
    phases[name] = {
      expectedProfile: expected?.id ?? null,
      expectedFramesPerSecond: expected ? rate(expected) : null,
      samples: entries.length,
      steadyMilliseconds: duration,
      steadyDecodedFramesPerSecond: decodedFPS,
      configuredFrameRatio: Number.isFinite(configuredFrameRatio)
        ? configuredFrameRatio
        : null,
      steadyFormatPassed,
      cadencePassed,
    };
    gates.expectedSteadyFormats &&= steadyFormatPassed;
    gates.configuredCadence &&= cadencePassed;
  }
  return {
    enabled: true,
    scope:
      "Automatic source-format observations and decoded browser dimensions/cadence; sampled media-time continuity, not physical capture latency.",
    networkProfile,
    minimumDwellMilliseconds,
    maximumSampleGapMilliseconds,
    gates,
    passed: Object.values(gates).every(Boolean),
    transitions,
    phases,
    failures: [...new Set(failures)],
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const [directory, expected] = process.argv.slice(2);
  if (!directory || !expected)
    throw new Error(
      "usage: report.mjs OUTPUT_DIRECTORY EXPECTED_NETWORK_PROFILE",
    );
  const samples = (await readFile(join(directory, "samples.jsonl"), "utf8"))
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const report = summarizeSourceFormats(samples, expected);
  await writeFile(
    join(directory, "source-formats.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  // The caller incorporates this report into its final verdict after collecting
  // teardown and resource evidence, including when these format gates fail.
}
