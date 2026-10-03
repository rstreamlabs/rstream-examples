import assert from "node:assert/strict"
import test from "node:test"
import {
  MediaMTXPlayback,
  RecordingNotFound,
  parseRecordingSpans,
  recordingClip,
  validatePlaybackURL,
} from "../src/lib/mediamtx-playback.ts"

const path = "devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11"
const options = {
  endpoint: "http://private.invalid/history",
  credential: (p) => {
    assert.equal(p, path)
    return "private-token"
  },
  windowSeconds: 300,
}
const signal = () => new AbortController().signal
const query = () =>
  new URLSearchParams({
    start: new Date(Date.now() - 60000).toISOString(),
    duration: "5",
  })
const empty = () => Response.json([])
const media = (body = "MP4", headers = {}) =>
  new Response(body, { headers: { "Content-Type": "video/mp4", ...headers } })

test("playback URL and request bounds reject credentials, query injection, duplicate and stale parameters", () => {
  assert.equal(
    validatePlaybackURL(options.endpoint).href,
    "http://private.invalid/history/",
  )
  for (const url of [
    "file:///recordings",
    "http://u:p@host",
    "https://host/?path=x",
    "http://host/#token",
  ])
    assert.throws(() => validatePlaybackURL(url))
  const now = Date.now(),
    start = new Date(now - 60000).toISOString()
  assert.deepEqual(
    recordingClip(new URLSearchParams({ start, duration: "0.125" }), now, 300),
    { start, duration: 0.125 },
  )
  for (const q of [
    new URLSearchParams({ start, duration: "31" }),
    new URLSearchParams({ start, duration: "0" }),
    new URLSearchParams({ start, duration: "Infinity" }),
    new URLSearchParams({ start, duration: "5", path: "other" }),
    new URLSearchParams({
      start: new Date(now - 301000).toISOString(),
      duration: "1",
    }),
    new URLSearchParams({ start: new Date(now).toISOString(), duration: "1" }),
  ])
    assert.throws(() => recordingClip(q, now, 300), RangeError)
  const duplicate = new URLSearchParams({ start, duration: "5" })
  duplicate.append("start", start)
  assert.throws(() => recordingClip(duplicate, now, 300), RangeError)
})

test("recording index clips to the recent window, ignores upstream URLs and preserves codec boundaries", () => {
  const now = Date.now(),
    iso = (x) => new Date(x).toISOString()
  assert.deepEqual(
    parseRecordingSpans(
      [
        {
          start: iso(now - 40000),
          duration: 20,
          url: "http://internal.invalid/get?secret=x",
        },
        {
          start: iso(now - 20000),
          duration: 30,
          url: "https://malicious.invalid",
        },
      ],
      now - 30000,
      now,
    ),
    [
      { start: iso(now - 30000), end: iso(now - 20000) },
      { start: iso(now - 20000), end: iso(now) },
    ],
  )
  for (const raw of [
    null,
    Array(257).fill({}),
    [{ start: "invalid", duration: 1 }],
    [{ start: iso(now), duration: NaN }],
    [{ start: iso(now), duration: -1 }],
    [
      { start: iso(now), duration: 5 },
      { start: iso(now + 1000), duration: 2 },
    ],
  ])
    assert.throws(() => parseRecordingSpans(raw, now - 30000, now))
})

test("index coalesces readers, isolates cancellation and returns independent cached results", async () => {
  let complete,
    calls = 0,
    upstream
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async (url, init) => {
      calls++
      upstream = init.signal
      assert.equal(url.pathname, "/history/list")
      assert.equal(url.searchParams.get("path"), path)
      assert.equal(init.headers.Authorization, "Bearer private-token")
      assert.equal(init.redirect, "error")
      assert.equal(init.cache, "no-store")
      return new Promise((resolve) => {
        complete = () => resolve(empty())
      })
    },
  })
  const controller = new AbortController()
  const first = client.index(path, controller.signal),
    second = client.index(path, signal())
  const rejected = assert.rejects(first)
  controller.abort()
  await rejected
  assert.equal(upstream.aborted, false)
  complete()
  const result = await second
  assert.equal(calls, 1)
  result.spans.push({ start: "changed", end: "changed" })
  assert.deepEqual((await client.index(path, signal())).spans, [])
})

test("last index cancellation aborts upstream and does not poison its next request", async () => {
  let requests = 0,
    aborted = false
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async (_url, init) => {
      if (++requests > 1) return empty()
      return new Promise((_, reject) =>
        init.signal.addEventListener(
          "abort",
          () => {
            aborted = true
            reject(new Error("private details"))
          },
          { once: true },
        ),
      )
    },
  })
  const controller = new AbortController(),
    pending = client.index(path, controller.signal)
  const failed = assert.rejects(pending)
  controller.abort()
  await failed
  await new Promise((resolve) => setImmediate(resolve))
  assert.equal(aborted, true)
  assert.deepEqual((await client.index(path, signal())).spans, [])
})

test("no recordings is an empty index; actual failures are generic and briefly cached", async () => {
  const absent = new MediaMTXPlayback({
    ...options,
    fetch: async () =>
      Response.json(
        { status: "error", error: "no recording segments found" },
        { status: 404 },
      ),
  })
  assert.deepEqual((await absent.index(path, signal())).spans, [])
  await assert.rejects(absent.clip(path, query(), signal()), RecordingNotFound)
  let calls = 0
  const broken = new MediaMTXPlayback({
    ...options,
    fetch: async () => {
      calls++
      return new Response("private secret", { status: 503 })
    },
  })
  for (let i = 0; i < 2; i++)
    await assert.rejects(broken.index(path, signal()), {
      message: "Recording service is unavailable",
    })
  assert.equal(calls, 1)
})

test("clip streams with backpressure, bounded admission and no forwarded headers or URLs", async () => {
  let reads = 0
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async (url, init) => {
      assert.equal(url.pathname, "/history/get")
      assert.equal(url.searchParams.get("path"), path)
      assert.equal(url.searchParams.get("format"), "mp4")
      assert.equal(init.credentials, "omit")
      return media(
        new ReadableStream(
          {
            pull(c) {
              reads++
              c.enqueue(new TextEncoder().encode("clip"))
              c.close()
            },
          },
          { highWaterMark: 0 },
        ),
        { "Set-Cookie": "secret=x", Location: "https://internal.invalid" },
      )
    },
  })
  const responses = await Promise.all(
    Array.from({ length: 4 }, () => client.clip(path, query(), signal())),
  )
  assert.equal(reads, 0)
  await assert.rejects(client.clip(path, query(), signal()))
  assert.equal(responses[0].headers.get("set-cookie"), null)
  assert.equal(responses[0].headers.get("location"), null)
  assert.equal(responses[0].headers.get("accept-ranges"), "none")
  assert.equal(await responses[0].text(), "clip")
  const fifth = await client.clip(path, query(), signal())
  await fifth.body.cancel()
  for (const response of responses.slice(1)) await response.body.cancel()
  assert.equal(reads, 1)
})

test("client disconnect cancels an unconsumed clip and frees its admission slot", async () => {
  let cancelled = 0,
    aborted = 0
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async (_url, init) => {
      init.signal.addEventListener("abort", () => aborted++, { once: true })
      return media(
        new ReadableStream(
          {
            cancel() {
              cancelled++
            },
          },
          { highWaterMark: 0 },
        ),
      )
    },
  })
  const controller = new AbortController()
  const response = await client.clip(path, query(), controller.signal)
  controller.abort()
  await assert.rejects(response.text(), {
    message: "Recording service is unavailable",
  })
  assert.equal(cancelled, 1)
  assert.equal(aborted, 1)
  const next = await client.clip(path, query(), signal())
  await next.body.cancel()
  assert.equal(cancelled, 2)
})

test("invalid and oversized media responses are cancelled before being exposed", async () => {
  for (const headers of [
    { "Content-Type": "text/html" },
    { "Content-Length": String(64 * 1024 * 1024 + 1) },
    { "Content-Length": "invalid" },
    { "Content-Encoding": "gzip" },
  ]) {
    let cancelled = false
    const client = new MediaMTXPlayback({
      ...options,
      fetch: async () =>
        media(
          new ReadableStream(
            {
              cancel() {
                cancelled = true
              },
            },
            { highWaterMark: 0 },
          ),
          headers,
        ),
    })
    await assert.rejects(client.clip(path, query(), signal()), {
      message: "Recording service is unavailable",
    })
    assert.equal(cancelled, true)
  }
})

test("index body size is bounded even without Content-Length", async () => {
  let cancelled = false
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async () =>
      new Response(
        new ReadableStream(
          {
            pull(c) {
              c.enqueue(new Uint8Array(65537))
            },
            cancel() {
              cancelled = true
            },
          },
          { highWaterMark: 0 },
        ),
        { headers: { "Content-Type": "application/json" } },
      ),
  })
  await assert.rejects(client.index(path, signal()))
  assert.equal(cancelled, true)
})

test("index deadline cancels a stalled body rather than leaving a shared request running", async () => {
  let cancelled = false
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async () =>
      new Response(
        new ReadableStream(
          {
            cancel() {
              cancelled = true
            },
          },
          { highWaterMark: 0 },
        ),
        { headers: { "Content-Type": "application/json" } },
      ),
  })
  const started = performance.now()
  await assert.rejects(client.index(path, signal()))
  assert.equal(cancelled, true)
  assert.ok(performance.now() - started < 6500)
})

test("only the pinned MediaMTX missing-directory response means an empty recording index", async () => {
  for (const [message, empty] of [
    [`lstat /recordings/${path}: no such file or directory`, true],
    [`lstat /recordings/${path}: permission denied`, false],
    ["lstat /different/path: no such file or directory", false],
    ["path is not configured", false],
  ]) {
    const client = new MediaMTXPlayback({
      ...options,
      fetch: async () =>
        Response.json({ status: "error", error: message }, { status: 400 }),
    })
    if (empty) assert.deepEqual((await client.index(path, signal())).spans, [])
    else await assert.rejects(client.index(path, signal()))
  }
})

test("an unrelated HTTP 404 is unavailable rather than a false empty history", async () => {
  const client = new MediaMTXPlayback({
    ...options,
    fetch: async () => new Response("Not found", { status: 404 }),
  })
  await assert.rejects(client.index(path, signal()), {
    message: "Recording service is unavailable",
  })
})
