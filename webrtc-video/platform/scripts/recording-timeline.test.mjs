import assert from "node:assert/strict"
import test from "node:test"
import {
  latestRecordingClip,
  parseRecordingWindow,
  readRecordingWindow,
  recordingClipURL,
  selectRecordingClip,
} from "../src/lib/recording-timeline.ts"

const end = Date.parse("2026-10-03T12:00:00Z")
const iso = (offset) => new Date(end + offset).toISOString()
const index = () => ({
  windowStart: iso(-300000),
  windowEnd: iso(0),
  maximumClipSeconds: 30,
  spans: [
    { start: iso(-280000), end: iso(-120000) },
    { start: iso(-60000), end: iso(-5000) },
  ],
})

test("recording selection is bounded by retention, gaps and codec boundaries", () => {
  const window = parseRecordingWindow(index(), 100)
  assert.deepEqual(selectRecordingClip(window, end - 140000, 100), {
    start: end - 140000,
    end: end - 120000,
  })
  assert.deepEqual(selectRecordingClip(window, end - 270000, 100), {
    start: end - 270000,
    end: end - 240000,
  })
  for (const at of [
    NaN,
    Infinity,
    end - 300000,
    end - 90000,
    end - 120000,
    end,
    end - 5100,
  ])
    assert.equal(selectRecordingClip(window, at, 100), null)
  assert.equal(
    selectRecordingClip(window, end - 280000, 21100),
    null,
    "Monotonic elapsed time expires old positions",
  )
  assert.deepEqual(latestRecordingClip(window, 100), {
    start: end - 15000,
    end: end - 5000,
  })
  assert.equal(latestRecordingClip(window, 400100), null)
  assert.equal(latestRecordingClip({ ...window, spans: [] }, 100), null)
})

test("index validation rejects unbounded, overlapping and malformed spans", () => {
  for (const override of [
    { windowStart: iso(-601000) },
    { windowEnd: "invalid" },
    { maximumClipSeconds: 31 },
    { maximumClipSeconds: Infinity },
    { spans: Array(257).fill({ start: iso(-1), end: iso(0) }) },
    { spans: [{ start: iso(-301000), end: iso(-1000) }] },
    { spans: [{ start: iso(-1000), end: iso(1000) }] },
    {
      spans: [
        { start: iso(-60000), end: iso(-5000) },
        { start: iso(-6000), end: iso(-1000) },
      ],
    },
  ])
    assert.throws(() => parseRecordingWindow({ ...index(), ...override }))
  assert.throws(() => parseRecordingWindow(null))
})

test("clip URL is same-origin, encodes the device ID and contains only time bounds", () => {
  const url = new URL(
    recordingClipURL("device/other?secret", { start: end - 1000, end }),
    "https://public.test",
  )
  assert.equal(
    url.pathname,
    "/api/devices/device%2Fother%3Fsecret/recordings/playback",
  )
  assert.deepEqual(
    [...url.searchParams],
    [
      ["start", iso(-1000)],
      ["duration", "1.000"],
    ],
  )
})

test("streamed index handles split UTF-8 and rejects oversized or invalid bodies", async () => {
  const bytes = new TextEncoder().encode(
    JSON.stringify({ ...index(), ignored: "é" }),
  )
  const response = new Response(
    new ReadableStream({
      start(controller) {
        for (const byte of bytes) controller.enqueue(Uint8Array.of(byte))
        controller.close()
      },
    }),
  )
  assert.equal(
    (await readRecordingWindow(response, new AbortController().signal)).spans
      .length,
    2,
  )
  for (const body of [new Uint8Array(65537), Uint8Array.of(255), "null"])
    await assert.rejects(
      readRecordingWindow(new Response(body), new AbortController().signal),
    )
})

test("aborting a blocked index read cancels and releases its stream", async () => {
  let cancelled = false
  const controller = new AbortController()
  const response = new Response(
    new ReadableStream({
      cancel() {
        cancelled = true
      },
    }),
  )
  const reading = readRecordingWindow(response, controller.signal)
  controller.abort()
  await assert.rejects(reading, { name: "AbortError" })
  assert.equal(cancelled, true)
  assert.equal(response.body.locked, false)
})
