import { readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const expectedGroups = [
  ["direct", "nack-rtx"],
  ["relay", "nack-rtx"],
  ["direct", "nack-rtx-flexfec"],
  ["relay", "nack-rtx-flexfec"],
];

export async function loadMatrix(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const runs = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) {
      continue;
    }
    const runDirectory = join(directory, entry.name);
    try {
      const [manifestRaw, summaryRaw] = await Promise.all([
        readFile(join(runDirectory, "manifest.json"), "utf8"),
        readFile(join(runDirectory, "summary.json"), "utf8"),
      ]);
      runs.push({
        directory: entry.name,
        manifest: JSON.parse(manifestRaw),
        summary: JSON.parse(summaryRaw),
      });
    } catch (error) {
      runs.push({
        directory: entry.name,
        error: normalizeError(error).message,
      });
    }
  }
  return runs.sort((left, right) =>
    left.directory.localeCompare(right.directory),
  );
}

export function compare(runs, minimumRepetitions = 1) {
  const groups = Object.fromEntries(
    expectedGroups.map(([path, profile]) => {
      const key = groupKey(path, profile);
      const matching = runs.filter(
        (run) =>
          !run.error &&
          run.manifest.networkPath?.kind === path &&
          run.manifest.protection?.profile === profile,
      );
      return [key, summarizeGroup(path, profile, matching)];
    }),
  );
  const incomplete = runs.filter((run) => run.error);
  const complete = runs.filter((run) => !run.error);
  const impairmentProfiles = complete.map((run) =>
    JSON.stringify(
      impairmentConditions(
        run.manifest.phases?.find((phase) => phase.name === "impaired")
          ?.shaping,
      ),
    ),
  );
  const impairment = complete[0]?.manifest.phases?.find(
    (phase) => phase.name === "impaired",
  )?.shaping;
  const fullDirect = groups[groupKey("direct", "nack-rtx-flexfec")];
  const fullRelay = groups[groupKey("relay", "nack-rtx-flexfec")];
  const baseRelay = groups[groupKey("relay", "nack-rtx")];
  const fullRuns = complete.filter(
    (run) => run.manifest.protection?.profile === "nack-rtx-flexfec",
  );
  const fullProtectionProfiles = fullRuns.map((run) =>
    JSON.stringify({
      mediaPackets: run.manifest.protection?.flexFECMediaPackets,
      repairPackets: run.manifest.protection?.flexFECRepairPackets,
    }),
  );
  const fullProtection = fullRuns[0]
    ? {
        mediaPackets: fullRuns[0].manifest.protection?.flexFECMediaPackets,
        repairPackets: fullRuns[0].manifest.protection?.flexFECRepairPackets,
      }
    : null;
  const assertions = [];
  assert(
    assertions,
    incomplete.length === 0,
    "complete-runs",
    "every matrix run produced a machine-readable summary",
  );
  assert(
    assertions,
    complete.length > 0 &&
      new Set(complete.map((run) => run.manifest.git?.revision)).size === 1 &&
      complete.every((run) => run.manifest.git?.revision),
    "single-revision",
    "every run qualifies the same repository revision",
  );
  assert(
    assertions,
    complete.length > 0 &&
      new Set(complete.map((run) => run.manifest.git?.producerTree)).size ===
        1 &&
      complete.every((run) => run.manifest.git?.producerTree),
    "single-producer-tree",
    "every run qualifies the same WebRTC producer source tree",
  );
  assert(
    assertions,
    complete.length > 0 &&
      complete.every((run) => run.manifest.git?.dirty === false),
    "clean-producer-tree",
    "the WebRTC producer source tree is clean for every run",
  );
  assert(
    assertions,
    complete.length > 0 && new Set(impairmentProfiles).size === 1 && impairment,
    "single-impairment-profile",
    "every run applies the same declared network conditions",
  );
  assert(
    assertions,
    fullRuns.length > 0 &&
      new Set(fullProtectionProfiles).size === 1 &&
      Number.isInteger(fullProtection?.mediaPackets) &&
      fullProtection.mediaPackets > 0 &&
      Number.isInteger(fullProtection?.repairPackets) &&
      fullProtection.repairPackets > 0 &&
      fullProtection.repairPackets <= fullProtection.mediaPackets,
    "single-full-protection-profile",
    "every full-profile run uses one explicit FlexFEC protection ratio",
  );
  for (const [key, group] of Object.entries(groups)) {
    assert(
      assertions,
      group.runs >= minimumRepetitions,
      `${key}-coverage`,
      `${key} has at least ${minimumRepetitions} complete run(s)`,
    );
  }
  assert(
    assertions,
    fullDirect.passedRuns === fullDirect.runs && fullDirect.runs > 0,
    "full-direct-pass",
    "the full protection profile passes every direct-reference run",
  );
  assert(
    assertions,
    fullRelay.passedRuns === fullRelay.runs && fullRelay.runs > 0,
    "full-relay-pass",
    "the full protection profile passes every rstream relay run",
  );
  assert(
    assertions,
    fullDirect.adaptationRuns === fullDirect.runs && fullDirect.runs > 0,
    "full-direct-adaptation-coverage",
    "every full-profile direct run forces and measures a congestion response",
  );
  assert(
    assertions,
    fullRelay.feedbackRuns === fullRelay.runs && fullRelay.runs > 0,
    "full-relay-feedback-coverage",
    "every full-profile relay run receives valid TWCC feedback and avoids increasing its target under additional pressure",
  );
  assert(
    assertions,
    fullRelay.impairedFPS.median >= 20,
    "full-relay-frame-rate",
    "full-profile rstream relay median impaired output stays at or above 20 fps",
  );
  assert(
    assertions,
    fullRelay.impairedFreezeRatio.median <= 0.1,
    "full-relay-freezes",
    "full-profile rstream relay median impaired freeze ratio stays at or below 10%",
  );
  assert(
    assertions,
    fullDirect.qualityRuns === fullDirect.runs &&
      fullRelay.qualityRuns === fullRelay.runs &&
      fullDirect.runs > 0 &&
      fullRelay.runs > 0,
    "full-profile-quality-coverage",
    "every full-profile direct and relay run reports H.264 quantization quality",
  );
  assert(
    assertions,
    fullRelay.impairedAverageQP.median <= 42,
    "full-relay-compression-quality",
    "full-profile rstream relay median impaired H.264 QP stays at or below 42",
  );
  assert(
    assertions,
    fullRelay.impairedFPS.median >= fullDirect.impairedFPS.median - 5,
    "relay-direct-frame-gap",
    "rstream relay stays within 5 fps of the direct reference under identical impairment",
  );
  assert(
    assertions,
    fullRelay.impairedFreezeRatio.median <=
      fullDirect.impairedFreezeRatio.median + 0.05,
    "relay-direct-freeze-gap",
    "rstream relay stays within five freeze-percentage points of the direct reference",
  );
  assert(
    assertions,
    fullRelay.impairedAverageQP.median <=
      fullDirect.impairedAverageQP.median + 6,
    "relay-direct-quality-gap",
    "rstream relay average H.264 QP stays within six points of the direct reference",
  );
  if (baseRelay.runs > 0) {
    const baselineDegraded =
      baseRelay.impairedFPS.median < 20 ||
      baseRelay.impairedFreezeRatio.median > 0.1;
    assert(
      assertions,
      baselineDegraded
        ? fullRelay.impairedFreezeRatio.median <=
            baseRelay.impairedFreezeRatio.median - 0.05 ||
            fullRelay.impairedFPS.median >= baseRelay.impairedFPS.median + 3
        : fullRelay.impairedFreezeRatio.median <=
            baseRelay.impairedFreezeRatio.median + 0.05 &&
            fullRelay.impairedFPS.median >= baseRelay.impairedFPS.median - 5,
      "proactive-repair-gain",
      baselineDegraded
        ? "proactive repair materially improves a degraded NACK/RTX relay baseline"
        : "proactive repair does not materially regress an already healthy NACK/RTX relay baseline",
    );
  }
  return {
    assertions,
    groups,
    impairment,
    incomplete,
    passed: assertions.every((assertion) => assertion.passed),
    protection: fullProtection,
    runs: runs.length,
  };
}

export function renderComparisonMarkdown(result, metadata = {}) {
  const rows = expectedGroups
    .map(([path, profile]) => {
      const group = result.groups[groupKey(path, profile)];
      return `| ${path} | ${profile} | ${group.passedRuns}/${group.runs} | ${range(group.impairedFPS, 1)} | ${range(group.impairedAverageQP, 1)} | ${percentRange(group.impairedFreezeRatio)} | ${range(group.impairedReceivedKbps, 0)} | ${range(group.maximumRTTMilliseconds, 0)} |`;
    })
    .join("\n");
  const assertions = result.assertions
    .map(
      (assertion) =>
        `- ${assertion.passed ? "PASS" : "FAIL"} — ${assertion.name}: ${assertion.description}`,
    )
    .join("\n");
  return `# Adaptive streaming direct/rstream matrix — ${result.passed ? "PASS" : "FAIL"}

Generated at ${metadata.generatedAt || new Date().toISOString()}${metadata.revision ? ` from repository revision \`${metadata.revision}\`` : ""}.

![Direct and rstream comparison](./comparison.svg)

## Impaired-link result

Every run applies the same outbound network profile: ${formatCapacity(result.impairment?.capacityKbps)} capacity, ${result.impairment?.delay || "unknown delay"}, ${result.impairment?.jitter || "unknown jitter"}, and ${result.impairment?.loss || "unknown loss"} random packet loss. Direct runs shape the peer media flow on an isolated Docker bridge. Relay runs force both peers through one managed TURN/UDP path and shape the producer-to-TURN transport. HTTP publication and rstream signaling are never shaped.

| Path | Protection | Passed runs | Decoded fps median [min–max] | Avg QP median [min–max] | Frozen median [min–max] | Received kbps median [min–max] | Max RTT ms median [min–max] |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
${rows}

## Acceptance criteria

${assertions}

The full profile means adaptive TWCC/GCC plus NACK, RTX, and ${result.protection?.repairPackets ?? "an unknown number of"} FlexFEC repair packet${result.protection?.repairPackets === 1 ? "" : "s"} per ${result.protection?.mediaPackets ?? "an unknown number of"} media packets. NACK/RTX-only runs remain in the matrix as a diagnostic baseline; the full-profile direct and rstream groups are the release qualification paths.
`;
}

export function renderComparisonSVG(result) {
  const groups = expectedGroups.map(([path, profile]) => ({
    label: `${path} ${profile === "nack-rtx" ? "NACK+RTX" : "+FlexFEC"}`,
    summary: result.groups[groupKey(path, profile)],
  }));
  const width = 1200;
  const height = 520;
  const panelWidth = 310;
  const panelGap = 75;
  const startX = 55;
  const baselineY = 430;
  const plotHeight = 310;
  const gap = 14;
  const barWidth = (panelWidth - gap * (groups.length - 1)) / groups.length;
  const bars = (field, maximum, panelOffset, color) =>
    groups
      .map((group, index) => {
        const value = group.summary[field].median;
        const barHeight = Math.max(
          0,
          Math.min(plotHeight, (value / maximum) * plotHeight),
        );
        const x = startX + panelOffset + index * (barWidth + gap);
        const y = baselineY - barHeight;
        const rendered =
          field === "impairedFPS"
            ? value.toFixed(1)
            : field === "impairedFreezeRatio"
              ? `${(value * 100).toFixed(1)}%`
              : value.toFixed(1);
        return `<rect x="${round(x)}" y="${round(y)}" width="${round(barWidth)}" height="${round(barHeight)}" rx="5" fill="${color}"/><text x="${round(x + barWidth / 2)}" y="${round(y - 10)}" text-anchor="middle" font-size="14" font-weight="700" fill="#111827">${rendered}</text><text transform="translate(${round(x + barWidth / 2)} ${baselineY + 18}) rotate(24)" text-anchor="start" font-size="12" fill="#374151">${escapeXML(group.label)}</text>`;
      })
      .join("");
  const passColor = result.passed ? "#047857" : "#b91c1c";
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description">
  <title id="title">Adaptive streaming direct and rstream comparison</title>
  <desc id="description">Decoded frames per second, H.264 quantization, and frozen-time percentage for direct and rstream paths under identical impairment.</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="${startX}" y="34" font-size="22" font-weight="700" fill="#111827">Adaptive video under ${escapeXML(result.impairment?.loss || "unknown loss")}, ${escapeXML(result.impairment?.delay || "unknown delay")}, and ${escapeXML(formatCapacity(result.impairment?.capacityKbps))}</text>
  <text x="${width - 60}" y="34" text-anchor="end" font-size="17" font-weight="700" fill="${passColor}">${result.passed ? "PASS" : "FAIL"}</text>
  <text x="${startX}" y="86" font-size="17" font-weight="600" fill="#111827">Decoded output (fps, higher is better)</text>
  <line x1="${startX}" y1="${baselineY}" x2="${startX + panelWidth}" y2="${baselineY}" stroke="#111827"/>
  ${bars("impairedFPS", 30, 0, "#2563eb")}
  <text x="${startX + panelWidth + panelGap}" y="86" font-size="17" font-weight="600" fill="#111827">Average QP (lower is better)</text>
  <line x1="${startX + panelWidth + panelGap}" y1="${baselineY}" x2="${startX + panelWidth * 2 + panelGap}" y2="${baselineY}" stroke="#111827"/>
  ${bars("impairedAverageQP", 51, panelWidth + panelGap, "#7c3aed")}
  <text x="${startX + (panelWidth + panelGap) * 2}" y="86" font-size="17" font-weight="600" fill="#111827">Frozen time (lower is better)</text>
  <line x1="${startX + (panelWidth + panelGap) * 2}" y1="${baselineY}" x2="${startX + panelWidth * 3 + panelGap * 2}" y2="${baselineY}" stroke="#111827"/>
  ${bars("impairedFreezeRatio", 0.35, (panelWidth + panelGap) * 2, "#d97706")}
</svg>
`;
}

export async function writeComparison(directory, result, metadata) {
  const compact = { ...result };
  await Promise.all([
    writeFile(
      join(directory, "comparison.json"),
      `${JSON.stringify(compact, null, 2)}\n`,
    ),
    writeFile(
      join(directory, "comparison.md"),
      renderComparisonMarkdown(result, metadata),
    ),
    writeFile(join(directory, "comparison.svg"), renderComparisonSVG(result)),
  ]);
}

function summarizeGroup(path, profile, runs) {
  const impaired = runs.map((run) => run.summary.phases.impaired);
  return {
    adaptationRuns: runs.filter((run) => run.summary.congestionResponseRequired)
      .length,
    feedbackRuns: runs.filter(
      (run) =>
        passedAssertion(run.summary, "twcc-feedback-integrity") &&
        passedAssertion(run.summary, "continued-pressure"),
    ).length,
    impairedAverageQP: distribution(impaired.map((phase) => phase.averageQP)),
    impairedFPS: distribution(
      impaired.map((phase) => phase.decodedFramesPerSecond),
    ),
    impairedFreezeRatio: distribution(
      impaired.map((phase) => phase.freezeRatio),
    ),
    impairedReceivedKbps: distribution(
      impaired.map((phase) => phase.medianReceivedBitrateKbps),
    ),
    maximumRTTMilliseconds: distribution(
      impaired.map((phase) => phase.maximumRTTMilliseconds),
    ),
    passedRuns: runs.filter((run) => run.summary.passed).length,
    path,
    profile,
    qualityRuns: impaired.filter((phase) => Number.isFinite(phase.averageQP))
      .length,
    runs: runs.length,
  };
}

function passedAssertion(summary, name) {
  return Boolean(
    summary.assertions?.find((assertion) => assertion.name === name)?.passed,
  );
}

function formatCapacity(capacityKbps) {
  if (!Number.isFinite(capacityKbps) || capacityKbps <= 0) {
    return "unknown capacity";
  }
  return `${Number((capacityKbps / 1000).toFixed(2))} Mbit/s`;
}

function distribution(values) {
  const finite = values
    .filter(Number.isFinite)
    .sort((left, right) => left - right);
  if (finite.length === 0) {
    return { maximum: 0, median: 0, minimum: 0 };
  }
  const middle = Math.floor(finite.length / 2);
  return {
    maximum: finite.at(-1),
    median:
      finite.length % 2 === 0
        ? (finite[middle - 1] + finite[middle]) / 2
        : finite[middle],
    minimum: finite[0],
  };
}

function groupKey(path, profile) {
  return `${path}-${profile}`;
}

function impairmentConditions(shaping) {
  if (!shaping) return null;
  const conditions = { ...shaping };
  delete conditions.scope;
  return conditions;
}

function assert(assertions, passed, name, description) {
  assertions.push({ description, name, passed: Boolean(passed) });
}

function range(summary, digits) {
  return `${summary.median.toFixed(digits)} [${summary.minimum.toFixed(digits)}–${summary.maximum.toFixed(digits)}]`;
}

function percentRange(summary) {
  return `${(summary.median * 100).toFixed(1)}% [${(summary.minimum * 100).toFixed(1)}–${(summary.maximum * 100).toFixed(1)}%]`;
}

function round(value) {
  return Math.round(value * 10) / 10;
}

function escapeXML(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function normalizeError(error) {
  return error instanceof Error ? error : new Error(String(error));
}
