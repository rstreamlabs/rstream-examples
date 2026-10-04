import assert from "node:assert/strict";
import test from "node:test";
import { decodeMarker } from "../latency/decode-marker.mjs";
import { installLatencyProbe } from "../latency/latency-probe.mjs";

// Independent known vector: timestamp 1791067642390908us, CRC from zlib.
const bytes = Buffer.from("5253010000065cf76e97e97cb229f8eb", "hex");
const rgba = new Uint8ClampedArray(256 * 32 * 4);
for (let bit = 0; bit < 128; bit++) {
  const value = bytes[bit >> 3] & (1 << (7 - (bit % 8))) ? 235 : 16;
  for (let y = 0; y < 8; y++)
    for (let x = 0; x < 8; x++) {
      const offset =
        ((Math.floor(bit / 32) * 8 + y) * 256 + (bit % 32) * 8 + x) * 4;
      rgba.set([value, value, value, 255], offset);
    }
}

test("timestamp decoding preserves microseconds and rejects a damaged checksum", () => {
  assert.equal(decodeMarker(rgba).timestampMilliseconds, 1791067642390.908);
  const damaged = rgba.slice();
  for (let y = 27; y <= 28; y++)
    for (let x = 251; x <= 252; x++) {
      const offset = (y * 256 + x) * 4;
      for (let c = 0; c < 3; c++)
        damaged[offset + c] = 255 - damaged[offset + c];
    }
  assert.equal(decodeMarker(damaged), null);
});

test("a paused collector stays bounded, drains once, and cancels late callbacks", () => {
  const previousDocument = globalThis.document;
  let draws = 0,
    requested = 0,
    cancelled = 0,
    callback;
  const canvas = {
    width: 0,
    height: 0,
    getContext: () => ({
      drawImage() {
        draws++;
      },
      getImageData: () => ({ data: rgba }),
    }),
  };
  globalThis.document = { createElement: () => canvas };
  const video = {
    requestVideoFrameCallback(fn) {
      callback = fn;
      return ++requested;
    },
    cancelVideoFrameCallback(id) {
      cancelled = id;
    },
  };
  try {
    const probe = installLatencyProbe(video);
    const metadata = {
      width: 640,
      height: 360,
      expectedDisplayTime: 10,
      rtpTimestamp: 500,
      mediaTime: 0,
    };
    for (let frame = 0; frame < 40; frame++) callback(frame * 200, metadata);
    const report = probe.read();
    assert.equal(report.samples.length, 32);
    assert.equal(report.omitted, 8);
    assert.equal(report.rejected, 0);
    assert.equal(
      report.samples[0].latencyMilliseconds,
      performance.timeOrigin + 10 - 1791067642390.908,
    );
    assert.equal(probe.read().samples.length, 0);
    // Calls faster than the configured sample rate don't read any pixels.
    callback(7801, metadata);
    assert.equal(draws, 40);
    const outstanding = callback,
      count = requested;
    probe.stop();
    probe.stop();
    assert.equal(cancelled, count);
    assert.equal(canvas.width, 0);
    outstanding(8000, metadata);
    assert.equal(requested, count);
    assert.equal(draws, 40);
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
});
