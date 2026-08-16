export function renderNetworkConditionsSVG(analysis, manifest) {
  const c = chartContext(analysis, manifest, 960, 650);
  const conditions = c.samples.map((sample) => conditionAt(sample, c));
  const specs = [
    [
      "Capacity",
      "Mb/s",
      "#be185d",
      conditions.map((v) =>
        v.capacityKbps === null ? null : v.capacityKbps / 1000,
      ),
      1,
    ],
    ["One-way delay", "ms", "#2563eb", conditions.map((v) => v.delayMs), 10],
    ["Jitter", "ms", "#d97706", conditions.map((v) => v.jitterMs), 5],
    [
      "Injected random loss",
      "%",
      "#dc2626",
      conditions.map((v) => v.lossPercent),
      1,
    ],
  ];
  const top = 145;
  const height = 88;
  const gap = 28;
  let body = networkConditionSummary(c);
  body += phaseBackgrounds(
    c,
    top,
    specs.length * height + (specs.length - 1) * gap,
  );
  specs.forEach(([label, unit, color, values, floor], index) => {
    const yTop = top + index * (height + gap);
    const max = Math.max(floor, finiteMaximum(values, floor));
    const y = yScale(yTop, height, max);
    body += panelFrame(c, yTop, height, label, unit);
    body += panelScale(c, yTop, height, max, unit);
    const field = ["capacityKbps", "delayMs", "jitterMs", "lossPercent"][index];
    const points = c.analysis.networkConditions?.available
      ? timelineStepPoints(c, field, y, field === "capacityKbps" ? 1000 : 1)
      : stepPoints(c, values, y);
    body += `<polyline fill="none" stroke="${color}" stroke-width="3" points="${points}"/>`;
    if (!c.analysis.networkConditions?.available) {
      body += changeLabels(c, values, y, unit);
    }
  });
  return documentSVG(
    c,
    "Controlled network conditions",
    "Capacity, one-way delay, jitter, and random packet loss applied by Linux traffic control to the selected media path.",
    body,
    "All inputs share the same elapsed-time axis. Empty spans are unshaped.",
  );
}

function networkConditionSummary(c) {
  if (!c.analysis.networkConditions?.available) return "";
  const changes = c.analysis.networkConditions.changes;
  const capacities = consecutiveUnique(
    changes.flatMap((change) =>
      Number.isFinite(change.capacityKbps) ? [change.capacityKbps / 1000] : [],
    ),
  );
  const impaired = changes.find((change) => change.name === "impaired-started");
  const capacityText = capacities
    .map((value) => format(value, value < 10 ? 1 : 0))
    .join(" → ");
  const impairmentText = impaired
    ? `${format(impaired.delayMs, 0)} ms one-way delay · ${format(impaired.jitterMs, 0)} ms jitter · ${format(impaired.lossPercent, 1)}% random loss`
    : "No impaired interval recorded";
  return `<text x="${c.left}" y="70" font-size="15" font-weight="600" fill="#111827">Applied capacity · ${xml(capacityText)} Mb/s</text><text x="${c.left}" y="94" font-size="14" fill="#475569">Impaired interval · ${xml(impairmentText)}</text>`;
}

export function renderPlaybackQualitySVG(analysis, manifest) {
  const c = chartContext(analysis, manifest, 960, 700);
  const fpsTop = 145;
  const fpsHeight = 155;
  const freezeTop = 340;
  const freezeHeight = 105;
  const qpTop = 485;
  const qpHeight = 145;
  let body = phaseBackgrounds(c, fpsTop, qpTop + qpHeight - fpsTop);
  const fps = c.samples.map((s) => numberOrNull(s.framesPerSecond));
  const fpsMaximum = Math.max(35, finiteMaximum(fps, 35));
  const fpsY = yScale(fpsTop, fpsHeight, fpsMaximum);
  body += panelFrame(c, fpsTop, fpsHeight, "Decoded frame rate", "fps");
  body += panelScale(c, fpsTop, fpsHeight, fpsMaximum, "fps");
  body += threshold(c, fpsY(25), "25 fps healthy", "#059669", "left");
  body += threshold(c, fpsY(20), "20 fps impaired", "#d97706", "left");
  body += line(c, fps, fpsY, "#2563eb");
  body += c.samples
    .map((sample, index) => {
      if (sample.playback === undefined || sample.playback === "Playing")
        return "";
      return `<circle cx="${round(c.x(sample.elapsedMilliseconds))}" cy="${round(fpsY(fps[index] || 0))}" r="4" fill="#dc2626"/>`;
    })
    .join("");
  const freezes = counterDeltas(c.samples, "totalFreezesDurationSeconds");
  const freezeTotal = freezes.reduce((sum, value) => sum + value, 0);
  const freezeMaximum = Math.max(0.1, finiteMaximum(freezes, 0.1));
  const freezeY = yScale(freezeTop, freezeHeight, freezeMaximum);
  body += panelFrame(c, freezeTop, freezeHeight, "Freeze duration", "s");
  body += panelScale(c, freezeTop, freezeHeight, freezeMaximum, "s");
  body += bars(c, freezes, freezeY, freezeTop, freezeHeight, "#dc2626", 6);
  body += `<text x="${c.left + 10}" y="${freezeTop + 25}" font-size="14" font-weight="600" fill="${freezeTotal === 0 ? "#047857" : "#b91c1c"}">${freezeTotal === 0 ? "No frozen playback interval observed" : `Total frozen playback ${format(freezeTotal, 2)} s`}</text>`;
  const qp = c.samples.map((sample) =>
    numberOrNull(analysis.phases[sample.phase]?.averageQP),
  );
  const qpY = yScale(qpTop, qpHeight, 51);
  body += panelFrame(c, qpTop, qpHeight, "H.264 QP", "QP");
  body += panelScale(c, qpTop, qpHeight, 51, "QP");
  body += threshold(c, qpY(42), "42 impaired ceiling", "#d97706", "left");
  body += `<polyline fill="none" stroke="#7c3aed" stroke-width="3" points="${stepPoints(c, qp, qpY)}"/>`;
  return documentSVG(
    c,
    "Playback continuity and visual quality",
    "Decoded frame rate, frozen playback, and phase-aligned encoder quantization with their acceptance limits.",
    body,
    "Red bars show browser-reported freeze duration. Red points identify a browser state other than Playing.",
  );
}

export function renderTransportEvidenceSVG(analysis, manifest) {
  const c = chartContext(analysis, manifest, 960, 840);
  const latencyTop = 145;
  const latencyHeight = 190;
  const repairTop = 375;
  const repairHeight = 140;
  const fecTop = 555;
  const fecHeight = 105;
  const lossTop = 700;
  const lossHeight = 80;
  let body = phaseBackgrounds(c, latencyTop, lossTop + lossHeight - latencyTop);
  const rtt = c.samples.map((s) =>
    Number.isFinite(s.currentRoundTripTimeSeconds)
      ? s.currentRoundTripTimeSeconds * 1000
      : null,
  );
  const pacer = c.samples.map((s) =>
    numberOrNull(s.pacerQueueDelayMilliseconds),
  );
  const playout = intervalRatios(
    c.samples,
    "jitterBufferDelaySeconds",
    "jitterBufferEmittedCount",
    1000,
  );
  const playoutTarget = intervalRatios(
    c.samples,
    "jitterBufferTargetDelaySeconds",
    "jitterBufferEmittedCount",
    1000,
  );
  const latencyMaximum = Math.max(
    600,
    finiteMaximum([...rtt, ...pacer, ...playout, ...playoutTarget], 600),
  );
  const latencyY = yScale(latencyTop, latencyHeight, latencyMaximum);
  body += panelFrame(c, latencyTop, latencyHeight, "Latency / buffering", "ms");
  body += rangeLabel(c, latencyTop, latencyMaximum, "ms");
  body += threshold(c, latencyY(600), "600 ms shaped RTT limit", "#dc2626");
  body += threshold(
    c,
    latencyY(300),
    "300 ms phase-average buffer limit",
    "#059669",
  );
  body += threshold(c, latencyY(250), "250 ms target limit", "#7c3aed");
  body += line(c, rtt, latencyY, "#2563eb");
  body += line(c, pacer, latencyY, "#d97706");
  body += line(c, playout, latencyY, "#059669");
  body += line(c, playoutTarget, latencyY, "#7c3aed");
  body += legend(c.left + 10, latencyTop + 25, [
    ["RTT", "#2563eb"],
    ["Pacer queue", "#d97706"],
    ["Effective buffer", "#059669"],
    ["Target buffer", "#7c3aed"],
  ]);
  const nack = counterDeltas(c.samples, "nackCount");
  const rtx = counterDeltas(c.samples, "retransmittedPacketsReceived");
  const repairY = yScale(
    repairTop,
    repairHeight,
    Math.max(1, finiteMaximum([...nack, ...rtx], 1)),
  );
  body += panelFrame(c, repairTop, repairHeight, "NACK / RTX", "packets");
  body += panelScale(
    c,
    repairTop,
    repairHeight,
    Math.max(1, finiteMaximum([...nack, ...rtx], 1)),
    "packets",
  );
  body += bars(c, nack, repairY, repairTop, repairHeight, "#dc2626", 4, -2);
  body += bars(c, rtx, repairY, repairTop, repairHeight, "#2563eb", 4, 2);
  body += legend(c.left + 10, repairTop + 25, [
    ["NACK", "#dc2626"],
    ["RTX received", "#2563eb"],
  ]);
  const fec = counterDeltas(c.samples, "fecPacketsReceived");
  const fecY = yScale(fecTop, fecHeight, Math.max(1, finiteMaximum(fec, 1)));
  body += panelFrame(c, fecTop, fecHeight, "FlexFEC received", "packets");
  body += panelScale(
    c,
    fecTop,
    fecHeight,
    Math.max(1, finiteMaximum(fec, 1)),
    "packets",
  );
  body += line(c, fec, fecY, "#059669");
  const observedLoss = intervalRatios(
    c.samples,
    "twccReportedLost",
    "twccReportedStatuses",
    100,
  );
  const injectedLoss = c.samples.map(
    (sample) => conditionAt(sample, c).lossPercent,
  );
  const lossY = yScale(
    lossTop,
    lossHeight,
    Math.max(5, finiteMaximum([...observedLoss, ...injectedLoss], 5)),
  );
  body += panelFrame(c, lossTop, lossHeight, "Injected / TWCC loss", "%");
  body += panelScale(
    c,
    lossTop,
    lossHeight,
    Math.max(5, finiteMaximum([...observedLoss, ...injectedLoss], 5)),
    "%",
  );
  body += line(c, injectedLoss, lossY, "#dc2626", "8 6");
  body += line(c, observedLoss, lossY, "#7c3aed");
  body += legend(c.left + 10, lossTop + 22, [
    ["Configured", "#dc2626"],
    ["TWCC observed", "#7c3aed"],
  ]);
  return documentSVG(
    c,
    "Transport latency, loss, and repair",
    "RTT, sender queue residence, playback buffering, NACK, RTX, FlexFEC, and TWCC loss aligned with the controlled path.",
    body,
    "Buffer trace is per sample; its gate is per phase. Counters are per-sample deltas.",
  );
}

function chartContext(analysis, manifest, width, height) {
  const left = 156;
  const right = 28;
  const samples = analysis.samples;
  const maximumTime = Math.max(1, ...samples.map((s) => s.elapsedMilliseconds));
  const phaseStarts = new Map();
  samples.forEach((sample) => {
    if (!phaseStarts.has(sample.phase))
      phaseStarts.set(sample.phase, sample.elapsedMilliseconds);
  });
  return {
    analysis,
    height,
    left,
    manifest,
    maximumTime,
    phaseStarts,
    right,
    samples,
    width,
    x: (milliseconds) =>
      left + (milliseconds / maximumTime) * (width - left - right),
  };
}

function conditionAt(sample, c) {
  if (c.analysis.networkConditions?.available) {
    const active = c.analysis.networkConditions.changes
      .filter(
        (change) => change.elapsedMilliseconds <= sample.elapsedMilliseconds,
      )
      .at(-1);
    if (!active) {
      return {
        capacityKbps: null,
        delayMs: null,
        jitterMs: null,
        lossPercent: null,
      };
    }
    return active;
  }
  const phase = c.manifest.phases.find(
    (candidate) => candidate.name === sample.phase,
  );
  const shaping = phase?.shaping;
  if (!shaping)
    return {
      capacityKbps: null,
      delayMs: null,
      jitterMs: null,
      lossPercent: null,
    };
  let active = shaping;
  if (Array.isArray(shaping.schedule) && shaping.schedule.length > 0) {
    const elapsed =
      (sample.elapsedMilliseconds - c.phaseStarts.get(sample.phase)) / 1000;
    let boundary = 0;
    active = shaping.schedule.at(-1);
    for (const step of shaping.schedule) {
      boundary += step.durationSeconds;
      if (elapsed < boundary) {
        active = step;
        break;
      }
    }
  }
  return {
    capacityKbps: finiteFallback(active.capacityKbps, shaping.capacityKbps),
    delayMs: durationMilliseconds(active.delay ?? shaping.delay),
    jitterMs: durationMilliseconds(active.jitter ?? shaping.jitter),
    lossPercent: parsePercent(active.loss ?? shaping.loss),
  };
}

function phaseBackgrounds(c, top, height) {
  return c.manifest.phases
    .map((phase, index) => {
      const samples = c.samples.filter((sample) => sample.phase === phase.name);
      if (samples.length === 0) return "";
      const start = c.x(samples[0].elapsedMilliseconds);
      const end = c.x(samples.at(-1).elapsedMilliseconds);
      const label = phase.name === "conditioning" ? "settling" : phase.name;
      return `<rect x="${round(start)}" y="${top}" width="${round(Math.max(1, end - start))}" height="${height}" fill="${index % 2 === 0 ? "#f8fafc" : "#eef2f7"}"/><text x="${round(start + 6)}" y="${top - 18}" font-size="14" font-weight="600" fill="#111827">${xml(label)}</text>`;
    })
    .join("");
}

function panelFrame(c, top, height, label, unit) {
  return `<line x1="${c.left}" y1="${top + height}" x2="${c.width - c.right}" y2="${top + height}" stroke="#cbd5e1"/><text x="${c.left - 12}" y="${top + 18}" text-anchor="end" font-size="14" font-weight="600" fill="#111827">${xml(label)}</text><text x="${c.left - 12}" y="${top + 38}" text-anchor="end" font-size="13" fill="#64748b">${xml(unit)}</text>`;
}

function panelScale(c, top, height, maximum, unit) {
  return [1, 0.5, 0]
    .map((ratio) => {
      const y = round(top + height * (1 - ratio));
      const value = maximum * ratio;
      return `<line x1="${c.left}" y1="${y}" x2="${c.width - c.right}" y2="${y}" stroke="#e2e8f0" stroke-width="1"/><text x="${c.width - c.right - 5}" y="${y - 4}" text-anchor="end" font-size="12" fill="#64748b">${format(value, value < 10 && value !== 0 ? 1 : 0)} ${xml(unit)}</text>`;
    })
    .join("");
}

function rangeLabel(c, top, maximum, unit) {
  return `<text x="${c.left - 12}" y="${top + 58}" text-anchor="end" font-size="12" fill="#64748b">0–${format(maximum, maximum < 10 ? 1 : 0)} ${xml(unit)}</text>`;
}

function yScale(top, height, maximum) {
  const max = Math.max(0.0001, maximum);
  return (value) =>
    top + height - (Math.max(0, Math.min(max, value)) / max) * height;
}

function line(c, values, y, color, dash = "") {
  const segments = [];
  let points = [];
  values.forEach((value, index) => {
    if (!Number.isFinite(value)) {
      if (points.length > 1) segments.push(points);
      points = [];
      return;
    }
    points.push(
      `${round(c.x(c.samples[index].elapsedMilliseconds))},${round(y(value))}`,
    );
  });
  if (points.length > 1) segments.push(points);
  return segments
    .map(
      (segment) =>
        `<polyline fill="none" stroke="${color}" stroke-width="2.5" stroke-dasharray="${dash}" points="${segment.join(" ")}"/>`,
    )
    .join("");
}

function stepPoints(c, values, y) {
  const points = [];
  let previous = null;
  values.forEach((value, index) => {
    if (!Number.isFinite(value)) {
      previous = null;
      return;
    }
    const x = round(c.x(c.samples[index].elapsedMilliseconds));
    if (Number.isFinite(previous) && previous !== value)
      points.push(`${x},${round(y(previous))}`);
    points.push(`${x},${round(y(value))}`);
    previous = value;
  });
  return points.join(" ");
}

function timelineStepPoints(c, field, y, divisor) {
  const points = [];
  let previous = null;
  for (const change of c.analysis.networkConditions.changes) {
    const raw = change[field];
    if (!Number.isFinite(raw)) {
      previous = null;
      continue;
    }
    const value = raw / divisor;
    const x = round(c.x(change.elapsedMilliseconds));
    if (Number.isFinite(previous) && previous !== value) {
      points.push(`${x},${round(y(previous))}`);
    }
    if (value !== previous) points.push(`${x},${round(y(value))}`);
    previous = value;
  }
  if (Number.isFinite(previous)) {
    points.push(`${round(c.x(c.maximumTime))},${round(y(previous))}`);
  }
  return points.join(" ");
}

function changeLabels(c, values, y, unit) {
  let previous = null;
  return values
    .map((value, index) => {
      if (!Number.isFinite(value) || value === previous) return "";
      previous = value;
      return `<text x="${round(c.x(c.samples[index].elapsedMilliseconds) + 5)}" y="${round(y(value) - 6)}" font-size="13" font-weight="600" fill="#111827">${format(value, value < 10 ? 1 : 0)} ${xml(unit)}</text>`;
    })
    .join("");
}

function consecutiveUnique(values) {
  return values.filter(
    (value, index) => index === 0 || value !== values[index - 1],
  );
}

function threshold(c, y, label, color, labelSide = "right") {
  const left = labelSide === "left";
  const x = left ? c.left + 7 : c.width - c.right - 5;
  return `<line x1="${c.left}" y1="${round(y)}" x2="${c.width - c.right}" y2="${round(y)}" stroke="${color}" stroke-width="1.5" stroke-dasharray="7 6"/><text x="${x}" y="${round(y - 5)}" text-anchor="${left ? "start" : "end"}" font-size="12" fill="${color}">${xml(label)}</text>`;
}

function bars(c, values, y, top, height, color, width = 6, offset = 0) {
  return values
    .map((value, index) => {
      if (!Number.isFinite(value) || value <= 0) return "";
      const x = c.x(c.samples[index].elapsedMilliseconds) + offset;
      return `<rect x="${round(x - width / 2)}" y="${round(y(value))}" width="${width}" height="${round(top + height - y(value))}" fill="${color}" opacity="0.82"/>`;
    })
    .join("");
}

function legend(x, y, entries) {
  return entries
    .map(([label, color], index) => {
      const start = x + index * 190;
      return `<line x1="${start}" y1="${y}" x2="${start + 24}" y2="${y}" stroke="${color}" stroke-width="3"/><text x="${start + 31}" y="${y + 5}" font-size="13" fill="#111827">${xml(label)}</text>`;
    })
    .join("");
}

function counterDeltas(samples, field) {
  return samples.map((sample, index) => {
    if (index === 0 || samples[index - 1].phase !== sample.phase) return null;
    const before = Number(samples[index - 1][field]);
    const after = Number(sample[field]);
    return Number.isFinite(before) && Number.isFinite(after)
      ? Math.max(0, after - before)
      : 0;
  });
}

function intervalRatios(samples, numeratorField, denominatorField, scale) {
  return samples.map((sample, index) => {
    if (index === 0 || samples[index - 1].phase !== sample.phase) return null;
    const numerator =
      Number(sample[numeratorField]) -
      Number(samples[index - 1][numeratorField]);
    const denominator =
      Number(sample[denominatorField]) -
      Number(samples[index - 1][denominatorField]);
    return Number.isFinite(numerator) &&
      Number.isFinite(denominator) &&
      numerator >= 0 &&
      denominator > 0
      ? (numerator / denominator) * scale
      : null;
  });
}

function finiteMaximum(values, fallback) {
  const finite = values.filter(Number.isFinite);
  return finite.length ? Math.max(...finite) : fallback;
}

function durationMilliseconds(value) {
  if (Number.isFinite(value)) return value;
  const match =
    typeof value === "string" ? value.match(/^([0-9]+(?:\.[0-9]+)?)ms$/) : null;
  return match ? Number(match[1]) : null;
}

function parsePercent(value) {
  if (Number.isFinite(value)) return value * 100;
  const match =
    typeof value === "string" ? value.match(/^([0-9]+(?:\.[0-9]+)?)%$/) : null;
  return match ? Number(match[1]) : null;
}

function finiteFallback(primary, fallback) {
  return Number.isFinite(primary)
    ? primary
    : Number.isFinite(fallback)
      ? fallback
      : null;
}

function numberOrNull(value) {
  return Number.isFinite(value) ? value : null;
}

function round(value) {
  return Math.round(value * 10) / 10;
}

function format(value, digits) {
  return Number.isFinite(value) ? value.toFixed(digits) : "n/a";
}

function xml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function documentSVG(c, title, description, body, footer) {
  const passed = c.analysis.passed;
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${c.width}" height="${c.height}" viewBox="0 0 ${c.width} ${c.height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">
<title id="title">${xml(title)}</title><desc id="description">${xml(description)}</desc>
<rect width="100%" height="100%" fill="#ffffff"/>
  <text x="28" y="36" font-size="24" font-weight="650" fill="#111827">${xml(title)}</text>
<text x="${c.width - c.right}" y="34" text-anchor="end" font-size="16" font-weight="700" fill="${passed ? "#047857" : "#b91c1c"}">${passed ? "PASS" : "FAIL"}</text>
${body}
<line x1="${c.left}" y1="${c.height - 55}" x2="${c.width - c.right}" y2="${c.height - 55}" stroke="#111827"/>
${timeAxis(c, c.height - 55)}
<text x="${c.width / 2}" y="${c.height - 8}" text-anchor="middle" font-size="12" fill="#64748b">${xml(footer)}</text>
</svg>
`;
}

function timeAxis(c, ordinate) {
  return Array.from({ length: 6 }, (_, index) => {
    const elapsed = (c.maximumTime * index) / 5;
    const x = round(c.x(elapsed));
    return `<line x1="${x}" y1="${ordinate}" x2="${x}" y2="${ordinate + 6}" stroke="#111827"/><text x="${x}" y="${ordinate + 22}" text-anchor="middle" font-size="12" fill="#4b5563">${Math.round(elapsed / 1000)} s</text>`;
  }).join("");
}
