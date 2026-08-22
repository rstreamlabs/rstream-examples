const width = 960;
const height = 460;
const margin = { top: 96, right: 28, bottom: 72, left: 78 };
const plotWidth = width - margin.left - margin.right;
const plotHeight = height - margin.top - margin.bottom;

export function renderFrameRateSVG(analysis) {
  const samples = analysis.samples.filter((sample) =>
    Number.isFinite(sample.framesPerSecond),
  );
  const maximumTime = lastElapsed(samples);
  const x = (sample) =>
    margin.left + (sample.elapsedMilliseconds / maximumTime) * plotWidth;
  const y = (value) => margin.top + plotHeight - (value / 35) * plotHeight;
  const points = samples
    .map((sample) => `${round(x(sample))},${round(y(sample.framesPerSecond))}`)
    .join(" ");
  const body = `${impairmentBand(samples, x)}
  ${verticalAxis([0, 10, 20, 30], y, "fps")}
  <line x1="${margin.left}" y1="${round(y(20))}" x2="${width - margin.right}" y2="${round(y(20))}" stroke="#dc2626" stroke-width="2" stroke-dasharray="7 6"/>
  <polyline fill="none" stroke="#2563eb" stroke-width="3" points="${points}"/>
  ${timeAxis(maximumTime)}`;
  return document(
    "Decoded frame rate",
    "Browser-decoded frames per second across the controlled congestion sequence.",
    body,
    [
      ["Decoded frame rate", "#2563eb", false],
      ["20 fps release floor", "#dc2626", true],
      ["Impaired link", "#fff7ed", false, true],
    ],
  );
}

export function renderPacketRepairSVG(analysis) {
  const phases = [
    ["baseline", "baseline"],
    ["constrained", "capacity limit"],
    ["impaired", "loss + delay"],
    ["recovery", "recovery"],
  ].map(([name, label]) => ({
    label,
    nack: analysis.phases[name]?.nackIncrease || 0,
    rtx: analysis.phases[name]?.retransmittedPacketsIncrease || 0,
  }));
  const maximum = Math.max(
    1,
    ...phases.flatMap((phase) => [phase.nack, phase.rtx]),
  );
  const ceiling = Math.ceil(maximum / 100) * 100 || 1;
  const y = (value) =>
    margin.top + plotHeight - (Math.min(value, ceiling) / ceiling) * plotHeight;
  const groupWidth = plotWidth / phases.length;
  const barWidth = Math.min(46, groupWidth / 3);
  const bars = phases
    .map((phase, index) => {
      const center = margin.left + groupWidth * (index + 0.5);
      return `${bar(center - barWidth - 3, phase.nack, y, barWidth, "#d97706")}
      ${bar(center + 3, phase.rtx, y, barWidth, "#2563eb")}
      <text x="${round(center)}" y="${margin.top + plotHeight + 28}" text-anchor="middle" font-size="16" fill="#475569">${phase.label}</text>`;
    })
    .join("\n");
  const body = `${verticalAxis([0, ceiling / 2, ceiling], y, "")}
  ${bars}
  <line x1="${margin.left}" y1="${margin.top + plotHeight}" x2="${width - margin.right}" y2="${margin.top + plotHeight}" stroke="#94a3b8"/>`;
  return document(
    "Packet loss triggers repair",
    "NACK requests and retransmitted packets received in each network phase.",
    body,
    [
      ["NACK requests", "#d97706", false],
      ["RTX received", "#2563eb", false],
    ],
  );
}

function document(title, description, body, series) {
  const legend = series
    .map(([label, color, dashed, band], index) => {
      const x = margin.left + index * 220;
      const mark = band
        ? `<rect x="${x}" y="63" width="24" height="14" rx="2" fill="${color}" stroke="#fed7aa"/>`
        : `<line x1="${x}" y1="70" x2="${x + 24}" y2="70" stroke="${color}" stroke-width="3"${dashed ? ' stroke-dasharray="7 6"' : ""}/>`;
      return `${mark}<text x="${x + 31}" y="76" font-size="16" fill="#111827">${label}</text>`;
    })
    .join("");
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">
  <title id="title">${title}</title>
  <desc id="description">${description}</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="${margin.left}" y="38" font-size="32" font-weight="750" fill="#111827">${title}</text>
  ${legend}
  ${body}
</svg>
`;
}

function verticalAxis(ticks, y, unit) {
  return ticks
    .map((value) => {
      const ordinate = round(y(value));
      const label = Number.isInteger(value) ? value : value.toFixed(1);
      return `<line x1="${margin.left}" y1="${ordinate}" x2="${width - margin.right}" y2="${ordinate}" stroke="#e2e8f0"/><text x="${margin.left - 12}" y="${ordinate + 5}" text-anchor="end" font-size="16" fill="#64748b">${label}${unit ? ` ${unit}` : ""}</text>`;
    })
    .join("\n  ");
}

function timeAxis(maximumTime) {
  const axisY = margin.top + plotHeight;
  const ticks = Array.from({ length: 5 }, (_, index) => {
    const elapsed = (maximumTime * index) / 4;
    const x = margin.left + (elapsed / maximumTime) * plotWidth;
    return `<text x="${round(x)}" y="${axisY + 28}" text-anchor="middle" font-size="16" fill="#475569">${Math.round(elapsed / 1000)} s</text>`;
  }).join("\n  ");
  return `<line x1="${margin.left}" y1="${axisY}" x2="${width - margin.right}" y2="${axisY}" stroke="#94a3b8"/>
  ${ticks}`;
}

function impairmentBand(samples, x) {
  const impaired = samples.filter((sample) => sample.phase === "impaired");
  if (impaired.length === 0) return "";
  const start = x(impaired[0]);
  const end = x(impaired.at(-1));
  return `<rect x="${round(start)}" y="${margin.top}" width="${round(end - start)}" height="${plotHeight}" fill="#fff7ed"/>`;
}

function bar(x, value, y, barWidth, color) {
  const top = y(value);
  const baseline = margin.top + plotHeight;
  return `<rect x="${round(x)}" y="${round(top)}" width="${round(barWidth)}" height="${round(Math.max(1, baseline - top))}" rx="4" fill="${color}"/>`;
}

function lastElapsed(samples) {
  return Math.max(1, ...samples.map((sample) => sample.elapsedMilliseconds));
}

function round(value) {
  return Math.round(value * 10) / 10;
}
