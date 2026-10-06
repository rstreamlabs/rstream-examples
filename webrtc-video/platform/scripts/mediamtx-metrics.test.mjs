import assert from "node:assert/strict"
import test from "node:test"
import { setTimeout as delay } from "node:timers/promises"
import {
  MediaMTXMetricsReader,
  parseMediaMTXMetrics,
  validateMetricsURL,
} from "../src/lib/mediamtx-metrics.ts"

const path = "devices/00000000-0000-4000-8000-000000000001"
const other = "devices/00000000-0000-4000-8000-000000000002"
const endpoint = "http://127.0.0.1:9998/metrics"
const signal = () => new AbortController().signal
const response = (body, options = {}) =>
  new Response(body, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
    ...options,
  })
function metrics({
  name = path,
  state = "ready",
  inbound = 1000,
  outbound = 2000,
  readers = 2,
} = {}) {
  const labels = `name="${name}",state="${state}"`
  return `# Paths\npaths{${labels}} 1\npaths_readers{${labels},readerType="webRTCSession"} ${readers}\npaths_inbound_bytes{${labels}} ${inbound}\npaths_outbound_bytes{${labels}} ${outbound}\npaths_inbound_frames_in_error{${labels}} 0\n`
}

test("metrics retain only the authorized path and sum reader types", () => {
  const body =
    metrics() +
    metrics({ name: other, inbound: 999999 }) +
    `paths_readers{state="ready",readerType="rtspSession",name="${path}"} 1\n`
  assert.deepEqual(parseMediaMTXMetrics(body, path), {
    state: "ready",
    readers: 3,
    inbound: 1000n,
    outbound: 2000n,
  })
  assert.deepEqual(parseMediaMTXMetrics("# Paths\n\n", path), {
    state: "idle",
    readers: 0,
    inbound: 0n,
    outbound: 0n,
  })
  assert.equal(
    parseMediaMTXMetrics(metrics({ state: "notReady", readers: 0 }), path)
      .state,
    "idle",
  )
  assert.equal(
    parseMediaMTXMetrics(metrics({ inbound: 9007199254740999n }), path).inbound,
    9007199254740999n,
  )
})

test("metrics reject incomplete, ambiguous or malformed upstream samples", () => {
  for (const body of [
    "This is not metrics",
    "<html>Unavailable</html>",
    metrics().replace("paths_inbound_bytes", "missing_counter"),
    metrics().replace("paths_readers", "missing_readers"),
    metrics() + metrics(),
    metrics().replace('state="ready"', 'state="unknown"'),
    metrics().replace('name="devices/', 'name="wrong",name="devices/'),
    metrics().replace(" 1000\n", " -1\n"),
    metrics().replace(" 1000\n", " NaN\n"),
    metrics({ inbound: 18446744073709551616n }),
    metrics({ readers: 9007199254740992n }),
  ])
    assert.throws(() => parseMediaMTXMetrics(body, path), /unavailable/)
})

test("metrics endpoint configuration is server-owned and unambiguous", () => {
  for (const raw of [
    endpoint,
    "http://mediamtx.internal:9998/metrics",
    "https://private.example/prefix/metrics",
  ])
    assert.ok(validateMetricsURL(raw))
  for (const raw of [
    "file:///metrics",
    "ftp://private/metrics",
    "http://user:secret@private/metrics",
    `${endpoint}?path=other`,
    `${endpoint}#fragment`,
    "http://private/whep",
  ])
    assert.throws(() => validateMetricsURL(raw))
})

test("rates require two recent samples; resets, idle, gaps and outages break the baseline", async () => {
  let now = 0,
    count = 0,
    body = metrics(),
    fail = false
  const reader = new MediaMTXMetricsReader({
    endpoint,
    clock: () => now,
    fetch: async (url, init) => {
      count++
      assert.equal(url.searchParams.get("path"), path)
      assert.equal(url.searchParams.get("type"), "paths")
      assert.equal(init.redirect, "error")
      if (fail) throw new Error("private diagnostics")
      return response(body)
    },
  })
  const first = await reader.read(path, signal())
  assert.equal(first.inboundBitsPerSecond, null)
  first.readers = 999 // A caller cannot mutate a cached observation.
  assert.equal((await reader.read(path, signal())).readers, 2)
  assert.equal(count, 1)
  now = 5000
  body = metrics({ inbound: 1001000, outbound: 2002000 })
  const second = await reader.read(path, signal())
  assert.equal(second.inboundBitsPerSecond, 1600000)
  assert.equal(second.outboundBitsPerSecond, 3200000)
  assert.equal(second.intervalMs, 5000)
  now += 5000
  body = metrics()
  assert.equal((await reader.read(path, signal())).inboundBitsPerSecond, null)
  now += 5000
  body = metrics({ state: "notReady", readers: 0 })
  assert.equal((await reader.read(path, signal())).inboundBitsPerSecond, null)
  now += 5000
  body = metrics()
  assert.equal((await reader.read(path, signal())).inboundBitsPerSecond, null)
  now += 20000
  assert.equal((await reader.read(path, signal())).inboundBitsPerSecond, null)
  now += 5000
  fail = true
  await assert.rejects(
    reader.read(path, signal()),
    /^Error: MediaMTX metrics are unavailable$/,
  )
  const failedCount = count
  await assert.rejects(reader.read(path, signal()))
  assert.equal(count, failedCount, "Failures also coalesce briefly")
  now += 5000
  fail = false
  assert.equal((await reader.read(path, signal())).inboundBitsPerSecond, null)
})

test("concurrent observers share a scrape; cancellation only stops it after the last waiter", async () => {
  let release,
    fetchSignal,
    calls = 0
  const reader = new MediaMTXMetricsReader({
    endpoint,
    fetch: async (_, init) => {
      calls++
      fetchSignal = init.signal
      await new Promise((resolve) => {
        release = resolve
      })
      return response(metrics())
    },
  })
  const a = new AbortController(),
    b = new AbortController()
  const one = reader.read(path, a.signal),
    two = reader.read(path, b.signal)
  a.abort()
  await assert.rejects(one, /abort/i)
  assert.equal(fetchSignal.aborted, false)
  release()
  assert.equal((await two).readers, 2)
  assert.equal(calls, 1)

  const controller = new AbortController()
  let cancelled = false
  const cancelReader = new MediaMTXMetricsReader({
    endpoint,
    fetch: async (_, init) =>
      new Promise((_, reject) => {
        init.signal.addEventListener(
          "abort",
          () => {
            cancelled = true
            reject(init.signal.reason)
          },
          { once: true },
        )
      }),
  })
  const pending = cancelReader.read(path, controller.signal)
  controller.abort()
  await assert.rejects(pending, /abort/i)
  assert.equal(cancelled, true)
})

test("scrapes and per-path waiters have hard admission limits", async () => {
  let calls = 0
  const reader = new MediaMTXMetricsReader({
    endpoint,
    fetch: async (_, init) => {
      calls++
      await new Promise((_, reject) =>
        init.signal.addEventListener(
          "abort",
          () => reject(init.signal.reason),
          { once: true },
        ),
      )
    },
  })
  const controller = new AbortController()
  const work = []
  // Attach rejection handlers immediately, before triggering cancellation.
  for (let index = 1; index <= 8; index++)
    work.push(
      reader
        .read(
          `devices/00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
          controller.signal,
        )
        .catch(() => {}),
    )
  await assert.rejects(
    reader.read("devices/00000000-0000-4000-8000-000000000009", signal()),
  )
  for (let index = 1; index < 64; index++)
    work.push(reader.read(path, controller.signal).catch(() => {}))
  await assert.rejects(reader.read(path, signal()))
  assert.equal(calls, 8)
  controller.abort()
  await Promise.all(work)
})

test("response status, type, byte bound and complete-body deadline are enforced", async () => {
  for (const make of [
    () => response("private reason", { status: 500 }),
    () =>
      new Response("<html>login</html>", {
        headers: { "Content-Type": "text/html" },
      }),
    () => response("x".repeat(32769)),
    () =>
      new Response("", {
        headers: { "Content-Type": "text/plain", "Content-Length": "32769" },
      }),
  ]) {
    const reader = new MediaMTXMetricsReader({
      endpoint,
      fetch: async () => make(),
    })
    await assert.rejects(reader.read(path, signal()), /unavailable/)
  }
  let cancelled = false
  const reader = new MediaMTXMetricsReader({
    endpoint,
    fetch: async () =>
      response(
        new ReadableStream({
          cancel() {
            cancelled = true
          },
        }),
      ),
  })
  const before = Date.now()
  await assert.rejects(reader.read(path, signal()), /unavailable/)
  assert.equal(cancelled, true)
  assert.ok(Date.now() - before < 4000)
  await delay(0)
})

test("an absent MediaMTX path returns an empty 200 without a content type", async () => {
  const reader = new MediaMTXMetricsReader({
    endpoint,
    fetch: async () =>
      new Response(null, { headers: { "Content-Length": "0" } }),
  })
  const result = await reader.read(path, signal())
  assert.equal(result.state, "idle")
  assert.equal(result.readers, 0)
  assert.equal(result.inboundBitsPerSecond, null)
})

test("the cache remains bounded as different devices are observed", async () => {
  let calls = 0
  const reader = new MediaMTXMetricsReader({
    endpoint,
    clock: () => 0,
    fetch: async (url) => {
      calls++
      return response(metrics({ name: url.searchParams.get("path") }))
    },
  })
  for (let index = 1; index <= 129; index++)
    await reader.read(
      `devices/00000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
      signal(),
    )
  await reader.read(path, signal())
  assert.equal(
    calls,
    130,
    "The oldest observation was evicted instead of retaining every device",
  )
})
