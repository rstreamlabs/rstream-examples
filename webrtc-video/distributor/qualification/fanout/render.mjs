import { readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const round = (value) => Math.round(value * 100) / 100;
const midpoint = (range) => (range.minimum + range.maximum) / 2;
const megabits = (range) => midpoint(range) / 1_000_000;

export function renderFanOutSVG(summary) {
  if (!summary?.publishable || summary.phases?.length !== 3) {
    throw new Error("fan-out evidence is not publishable");
  }
  const width = 960;
  const height = 520;
  const left = 112;
  const right = 910;
  const top = 126;
  const bottom = 420;
  const maximum = 70;
  const x = (index) => left + (index * (right - left)) / 2;
  const y = (value) => bottom - (value / maximum) * (bottom - top);
  const inbound = summary.phases.map((phase) => megabits(phase.inboundBitsPerSecond));
  const outbound = summary.phases.map((phase) => megabits(phase.outboundBitsPerSecond));
  const path = (values) =>
    values.map((value, index) => `${index === 0 ? "M" : "L"}${round(x(index))},${round(y(value))}`).join(" ");
  const points = (values, color) =>
    values
      .map(
        (value, index) =>
          `<circle cx="${round(x(index))}" cy="${round(y(value))}" r="7" fill="${color}"/>\n  <text x="${round(x(index))}" y="${round(y(value) - 14)}" text-anchor="middle" font-size="17" font-weight="700" fill="#0f172a">${value.toFixed(2)}</text>`,
      )
      .join("\n  ");
  const grid = [0, 10, 20, 30, 40, 50, 60, 70]
    .map(
      (value) =>
        `<line x1="${left}" y1="${round(y(value))}" x2="${right}" y2="${round(y(value))}" stroke="#e2e8f0"/>\n  <text x="${left - 14}" y="${round(y(value) + 6)}" text-anchor="end" font-size="16" fill="#64748b">${value}</text>`,
    )
    .join("\n  ");
  const labels = summary.phases
    .map(
      (phase, index) =>
        `<text x="${round(x(index))}" y="452" text-anchor="middle" font-size="18" font-weight="650" fill="#0f172a">${phase.readers} viewer${phase.readers === 1 ? "" : "s"}</text>`,
    )
    .join("\n  ");
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description" style="font-family:system-ui,sans-serif">
  <title id="title">One source serves multiple viewers</title>
  <desc id="description">Device ingress remains constant while MediaMTX egress grows with the number of viewers.</desc>
  <rect width="100%" height="100%" fill="#ffffff"/>
  <text x="28" y="42" font-size="32" font-weight="750" fill="#0f172a">One source serves multiple viewers</text>
  <text x="28" y="76" font-size="18" fill="#475569">Aggregate throughput · Mbit/s</text>
  <line x1="574" y1="68" x2="610" y2="68" stroke="#2563eb" stroke-width="5"/>
  <text x="620" y="75" font-size="17" fill="#475569">Device ingress</text>
  <line x1="752" y1="68" x2="788" y2="68" stroke="#059669" stroke-width="5"/>
  <text x="798" y="75" font-size="17" fill="#475569">Viewer egress</text>
  ${grid}
  <path d="${path(inbound)}" fill="none" stroke="#2563eb" stroke-width="5" stroke-linejoin="round"/>
  <path d="${path(outbound)}" fill="none" stroke="#059669" stroke-width="5" stroke-linejoin="round"/>
  ${points(inbound, "#2563eb")}
  ${points(outbound, "#059669")}
  ${labels}
  <text x="28" y="500" font-size="17" fill="#475569">The device publishes one stream; distribution capacity grows at MediaMTX.</text>
</svg>
`;
}

async function main() {
  const [input, output] = process.argv.slice(2);
  if (!input || !output) {
    throw new Error("usage: node render.mjs SUMMARY_JSON OUTPUT_SVG");
  }
  const summary = JSON.parse(await readFile(input, "utf8"));
  await writeFile(output, renderFanOutSVG(summary));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
