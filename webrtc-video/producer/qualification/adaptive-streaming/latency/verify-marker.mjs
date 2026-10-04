import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { decodeMarker } from "./decode-marker.mjs";
import { monotonicMilliseconds } from "./clock.mjs";

const runtime = mkdtempSync(join(tmpdir(), "rstream-latency-stamp-"));
try {
  assert.ok(
    process.env.GST_PLUGIN_PATH,
    "Set GST_PLUGIN_PATH to the compiled qualification plugin directory",
  );
  for (const bitrate of [500, 8000]) {
    const file = join(runtime, `frame-${bitrate}.rgba`);
    const before = monotonicMilliseconds();
    execFileSync(
      "gst-launch-1.0",
      [
        "-q",
        "videotestsrc",
        "num-buffers=1",
        "pattern=smpte",
        "!",
        "video/x-raw,format=I420,width=640,height=360,framerate=30/1",
        "!",
        "rstreamlatencystamp",
        "!",
        "x264enc",
        "tune=zerolatency",
        "speed-preset=veryfast",
        `bitrate=${bitrate}`,
        "!",
        "h264parse",
        "!",
        "avdec_h264",
        "!",
        "videoconvert",
        "!",
        "video/x-raw,format=RGBA",
        "!",
        "filesink",
        `location=${file}`,
      ],
      {
        timeout: 10000,
        env: process.env,
      },
    );
    const after = monotonicMilliseconds();
    const frame = readFileSync(file);
    assert.equal(frame.length, 640 * 360 * 4);
    const crop = new Uint8Array(256 * 32 * 4);
    for (let y = 0; y < 32; y++) {
      const start = ((y + 16) * 640 + 16) * 4;
      crop.set(frame.subarray(start, start + 256 * 4), y * 256 * 4);
    }
    const marker = decodeMarker(crop);
    assert.ok(marker, `Marker survives H.264 at ${bitrate} kbit/s`);
    assert.ok(marker.timestampMilliseconds >= before - 1);
    assert.ok(marker.timestampMilliseconds <= after + 1);
    // Corrupt a timestamp bit, not its preamble: integrity must reject it.
    const corrupt = crop.slice();
    for (let y = 11; y <= 12; y++)
      for (let x = 3; x <= 4; x++) {
        const start = (y * 256 + x) * 4;
        for (let channel = 0; channel < 3; channel++)
          corrupt[start + channel] = 255 - corrupt[start + channel];
      }
    assert.equal(decodeMarker(corrupt), null);
    const ambiguous = crop.slice();
    for (let y = 11; y <= 12; y++)
      for (let x = 3; x <= 4; x++) {
        const start = (y * 256 + x) * 4;
        ambiguous.fill(127, start, start + 3);
      }
    assert.equal(decodeMarker(ambiguous), null);
    console.log(
      JSON.stringify({ bitrate, marker, check: "encode-decode-only" }),
    );
  }
  assert.throws(() => decodeMarker(new Uint8Array(4)), TypeError);
} finally {
  rmSync(runtime, { recursive: true, force: true });
}
