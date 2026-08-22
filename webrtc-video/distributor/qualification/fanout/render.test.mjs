import assert from "node:assert/strict";
import test from "node:test";
import { renderFanOutSVG } from "./render.mjs";

const range = (minimum, maximum = minimum) => ({ minimum, maximum });

test("renders constant source ingress and viewer-scaled egress", () => {
  const svg = renderFanOutSVG({
    phases: [
      { readers: 1, inboundBitsPerSecond: range(8_000_000), outboundBitsPerSecond: range(8_000_000) },
      { readers: 4, inboundBitsPerSecond: range(8_000_000), outboundBitsPerSecond: range(32_000_000) },
      { readers: 8, inboundBitsPerSecond: range(8_000_000), outboundBitsPerSecond: range(64_000_000) },
    ],
    publishable: true,
  });
  assert.match(svg, /width="960" height="520"/);
  assert.match(svg, /One source serves multiple viewers/);
  assert.match(svg, /8\.00/);
  assert.match(svg, /32\.00/);
  assert.match(svg, /64\.00/);
  assert.doesNotMatch(svg, />PASS</);
});

test("rejects incomplete or non-publishable evidence", () => {
  assert.throws(() => renderFanOutSVG({ phases: [], publishable: true }));
  assert.throws(() => renderFanOutSVG({ phases: [{}, {}, {}], publishable: false }));
});
