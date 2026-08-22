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
    complete.length > 0 &&
      new Set(complete.map((run) => run.manifest.producerImage)).size === 1 &&
      complete.every((run) => run.manifest.producerImage),
    "single-producer-image",
    "every run qualifies the same producer container image",
  );
  assert(
    assertions,
    complete.length > 0 &&
      new Set(complete.map((run) => run.manifest.browserImage)).size === 1 &&
      complete.every((run) => run.manifest.browserImage),
    "single-browser-image",
    "every run qualifies the same browser container image",
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
  const width = 720;
  const height = 1280;
  const startX = 28;
  const plotLeft = 190;
  const plotRight = width - 32;
  const plotWidth = plotRight - plotLeft;
  const panelTop = 142;
  const panelHeight = 330;
  const panel = (definition, index) => {
    const top = panelTop + index * panelHeight;
    const scale = (value) =>
      plotLeft +
      Math.max(
        0,
        Math.min(plotWidth, (value / definition.maximum) * plotWidth),
      );
    const gateX = scale(definition.gate);
    const bars = groups
      .map((group, groupIndex) => {
        const distribution = group.summary[definition.field];
        const barY = top + 86 + groupIndex * 49;
        const centerY = barY + 15;
        const medianX = scale(distribution.median);
        const maximumX = scale(distribution.maximum);
        const minimumX = scale(distribution.minimum);
        const [pathLabel, profileLabel] = group.label.split(" ");
        const labelInside = medianX > plotRight - 90;
        const labelX = labelInside
          ? medianX - 10
          : Math.min(plotRight - 6, Math.max(medianX, maximumX) + 10);
        const labelAnchor = labelInside ? "end" : "start";
        const labelColor = labelInside ? "#ffffff" : "#0f172a";
        return `<text x="${startX}" y="${barY + 21}" font-size="19" font-weight="650" fill="#0f172a">${pathLabel}</text>
    <text x="${startX + 72}" y="${barY + 21}" font-size="18" fill="#475569">${profileLabel}</text>
    <rect x="${plotLeft}" y="${barY}" width="${round(Math.max(1, medianX - plotLeft))}" height="30" rx="5" fill="${group.color}" fill-opacity="0.9"/>
    <line x1="${round(minimumX)}" y1="${centerY}" x2="${round(maximumX)}" y2="${centerY}" stroke="#0f172a" stroke-width="3"/>
    <line x1="${round(minimumX)}" y1="${centerY - 7}" x2="${round(minimumX)}" y2="${centerY + 7}" stroke="#0f172a" stroke-width="3"/>
    <line x1="${round(maximumX)}" y1="${centerY - 7}" x2="${round(maximumX)}" y2="${centerY + 7}" stroke="#0f172a" stroke-width="3"/>
    <text x="${round(labelX)}" y="${barY + 22}" text-anchor="${labelAnchor}" font-size="18" font-weight="750" fill="${labelColor}">${definition.format(distribution.median)}</text>`;
      })
      .join("\n    ");
    return `<rect x="${startX}" y="${top}" width="${width - startX * 2}" height="${panelHeight - 18}" rx="12" fill="#f8fafc" stroke="#e2e8f0"/>
  <text x="${startX + 18}" y="${top + 34}" font-size="26" font-weight="750" fill="#0f172a">${definition.title}</text>
  <text x="${startX + 18}" y="${top + 61}" font-size="18" fill="#475569">${definition.unit}</text>
  <line x1="${plotLeft}" y1="${top + 74}" x2="${plotLeft}" y2="${top + 282}" stroke="#cbd5e1"/>
  <line x1="${plotRight}" y1="${top + 74}" x2="${plotRight}" y2="${top + 282}" stroke="#cbd5e1"/>
  <line x1="${round(gateX)}" y1="${top + 74}" x2="${round(gateX)}" y2="${top + 282}" stroke="#dc2626" stroke-width="2" stroke-dasharray="7 6"/>
  <text x="${round(gateX - 6)}" y="${top + 72}" text-anchor="end" font-size="17" font-weight="650" fill="#b91c1c">${definition.gateLabel}</text>
  <text x="${plotLeft}" y="${top + 304}" font-size="16" fill="#64748b">0</text>
  <text x="${plotRight}" y="${top + 304}" text-anchor="end" font-size="16" fill="#64748b">${definition.format(definition.maximum)}</text>
  ${bars}`;
  };
  const passColor = result.passed ? "#047857" : "#b91c1c";
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">
  <title id="title">Adaptive streaming direct and rstream comparison</title>
  <desc id="description">Median decoded frames per second, H.264 quantization, and frozen-time percentage with minimum and maximum whiskers across three direct and three rstream relay runs per protection profile.</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="${startX}" y="38" font-size="28" font-weight="750" fill="#0f172a">Direct and relay media quality</text>
  <text x="${startX}" y="76" font-size="19" fill="#475569">${escapeXML(formatCapacity(result.impairment?.capacityKbps))} · ${escapeXML(result.impairment?.delay || "unknown delay")} one-way delay · ${escapeXML(result.impairment?.jitter || "unknown jitter")} jitter · ${escapeXML(result.impairment?.loss || "unknown loss")} loss</text>
  <text x="${startX}" y="106" font-size="18" fill="#64748b">Medians and min–max across three independent runs</text>
  <text x="${width - startX}" y="38" text-anchor="end" font-size="22" font-weight="750" fill="${passColor}">${result.passed ? "PASS" : "FAIL"}</text>
  ${panels.map(panel).join("\n  ")}
  <text x="${startX}" y="1167" font-size="18" fill="#475569">Base · NACK + RTX</text>
  <text x="${startX}" y="1197" font-size="18" fill="#475569">Full · NACK + RTX + one FlexFEC packet per five media packets</text>
  <text x="${startX}" y="1237" font-size="17" fill="#64748b">Bars show medians; whiskers show min–max; dashed red lines are release gates.</text>
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
