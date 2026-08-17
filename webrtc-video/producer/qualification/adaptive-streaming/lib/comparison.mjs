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
  const colors = ["#475569", "#d97706", "#2563eb", "#059669"];
  const groups = expectedGroups.map(([path, profile], index) => ({
    color: colors[index],
    label: `${path === "direct" ? "Direct" : "Relay"} ${profile === "nack-rtx" ? "base" : "full"}`,
    summary: result.groups[groupKey(path, profile)],
  }));
  const panels = [
    {
      field: "impairedFPS",
      format: (value) => value.toFixed(1),
      gate: 20,
      gateLabel: "20 fps gate",
      maximum: 30,
      title: "Decoded output",
      unit: "fps · higher is better",
    },
    {
      field: "impairedAverageQP",
      format: (value) => value.toFixed(1),
      gate: 42,
      gateLabel: "QP 42 gate",
      maximum: 51,
      title: "H.264 quantization",
      unit: "average QP · lower is better",
    },
    {
      field: "impairedFreezeRatio",
      format: (value) => `${(value * 100).toFixed(1)}%`,
      gate: 0.1,
      gateLabel: "10% gate",
      maximum: 0.35,
      title: "Frozen time",
      unit: "share of phase · lower is better",
    },
  ];
  const width = 1200;
  const height = 620;
  const startX = 45;
  const panelWidth = 330;
  const panelGap = 55;
  const plotTop = 145;
  const baselineY = 475;
  const plotHeight = baselineY - plotTop;
  const barWidth = 56;
  const barGap = 21;
  const panel = (definition, index) => {
    const x = startX + index * (panelWidth + panelGap);
    const scale = (value) =>
      baselineY -
      Math.max(
        0,
        Math.min(plotHeight, (value / definition.maximum) * plotHeight),
      );
    const gateY = scale(definition.gate);
    const bars = groups
      .map((group, groupIndex) => {
        const distribution = group.summary[definition.field];
        const barX = x + 10 + groupIndex * (barWidth + barGap);
        const centerX = barX + barWidth / 2;
        const medianY = scale(distribution.median);
        const maximumY = scale(distribution.maximum);
        const minimumY = scale(distribution.minimum);
        const [pathLabel, profileLabel] = group.label.split(" ");
        return `<rect x="${round(barX)}" y="${round(medianY)}" width="${barWidth}" height="${round(baselineY - medianY)}" rx="5" fill="${group.color}" fill-opacity="0.88"/>
    <line x1="${round(centerX)}" y1="${round(maximumY)}" x2="${round(centerX)}" y2="${round(minimumY)}" stroke="#0f172a" stroke-width="2"/>
    <line x1="${round(centerX - 8)}" y1="${round(maximumY)}" x2="${round(centerX + 8)}" y2="${round(maximumY)}" stroke="#0f172a" stroke-width="2"/>
    <line x1="${round(centerX - 8)}" y1="${round(minimumY)}" x2="${round(centerX + 8)}" y2="${round(minimumY)}" stroke="#0f172a" stroke-width="2"/>
    <text x="${round(centerX)}" y="${round(Math.min(medianY, maximumY) - 10)}" text-anchor="middle" font-size="14" font-weight="700" fill="#0f172a">${definition.format(distribution.median)}</text>
    <text x="${round(centerX)}" y="502" text-anchor="middle" font-size="12" font-weight="600" fill="#0f172a">${pathLabel}</text>
    <text x="${round(centerX)}" y="519" text-anchor="middle" font-size="12" fill="#475569">${profileLabel}</text>`;
      })
      .join("\n    ");
    return `<text x="${x}" y="104" font-size="18" font-weight="700" fill="#0f172a">${definition.title}</text>
  <text x="${x}" y="127" font-size="13" fill="#475569">${definition.unit}</text>
  <line x1="${x}" y1="${baselineY}" x2="${x + panelWidth}" y2="${baselineY}" stroke="#94a3b8"/>
  <line x1="${x}" y1="${round(gateY)}" x2="${x + panelWidth}" y2="${round(gateY)}" stroke="#dc2626" stroke-width="1.5" stroke-dasharray="6 5"/>
  <rect x="${x + panelWidth - 90}" y="${round(gateY - 18)}" width="90" height="18" fill="#ffffff"/>
  <text x="${x + panelWidth - 4}" y="${round(gateY - 5)}" text-anchor="end" font-size="12" font-weight="600" fill="#b91c1c">${definition.gateLabel}</text>
  ${bars}`;
  };
  const passColor = result.passed ? "#047857" : "#b91c1c";
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description">
  <title id="title">Adaptive streaming direct and rstream comparison</title>
  <desc id="description">Median decoded frames per second, H.264 quantization, and frozen-time percentage with minimum and maximum whiskers across three direct and three rstream relay runs per protection profile.</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="${startX}" y="34" font-size="22" font-weight="700" fill="#0f172a">Adaptive video through direct and rstream relay paths</text>
  <text x="${startX}" y="61" font-size="14" fill="#475569">${escapeXML(formatCapacity(result.impairment?.capacityKbps))} · ${escapeXML(result.impairment?.delay || "unknown delay")} one-way delay · ${escapeXML(result.impairment?.jitter || "unknown jitter")} jitter · ${escapeXML(result.impairment?.loss || "unknown loss")} loss · medians and min–max across three runs</text>
  <text x="${width - 45}" y="34" text-anchor="end" font-size="17" font-weight="700" fill="${passColor}">${result.passed ? "PASS" : "FAIL"}</text>
  ${panels.map(panel).join("\n  ")}
  <text x="${startX}" y="566" font-size="13" fill="#475569">Base: NACK + RTX</text>
  <text x="${startX + 155}" y="566" font-size="13" fill="#475569">Full: NACK + RTX + one FlexFEC repair packet per five media packets</text>
  <text x="${startX}" y="594" font-size="12" fill="#64748b">Bars show the median. Whiskers show the minimum and maximum selected result; red lines are release gates.</text>
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
