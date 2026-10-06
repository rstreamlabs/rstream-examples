import assert from "node:assert/strict"
import test from "node:test"
import { mkdtemp, readFile, rm } from "node:fs/promises"
import { join } from "node:path"
import { tmpdir } from "node:os"

test("qualification observes recording release without consuming bodies or recording credentials", async () => {
  const directory = await mkdtemp(join(tmpdir(), "recording-observer-test-"))
  const trace = join(directory, "trace.jsonl")
  const previous = process.env.RSTREAM_QUALIFICATION_RECORDING_TRACE
  const original = globalThis.fetch
  const response = new Response("not consumed")
  globalThis.fetch = async () => response
  process.env.RSTREAM_QUALIFICATION_RECORDING_TRACE = trace
  try {
    await import("../qualification/end-to-end/recording-fetch-observer.mjs")
    const controller = new AbortController()
    const url =
      "http://127.0.0.1:9996/get?path=devices/camera&start=2026-10-03T12:00:00Z&duration=10&token=private-value"
    assert.equal(
      await fetch(url, {
        signal: controller.signal,
        headers: { Authorization: "Bearer private-value" },
      }),
      response,
    )
    assert.equal(response.bodyUsed, false)
    controller.abort()
    await fetch("https://unrelated.test/get", {
      signal: new AbortController().signal,
    })
    await fetch("http://127.0.0.1:9996/list?path=devices/camera")
    const text = await readFile(trace, "utf8")
    assert.ok(!text.includes("private-value"))
    const events = text.trim().split("\n").map(JSON.parse)
    assert.deepEqual(
      events.map((event) => event.phase),
      ["opened", "released"],
    )
    assert.equal(events[0].id, events[1].id)
    assert.equal(events[0].path, "devices/camera")
    assert.equal(events[0].duration, 10)
    assert.equal(response.bodyUsed, false)
  } finally {
    globalThis.fetch = original
    if (previous === undefined)
      delete process.env.RSTREAM_QUALIFICATION_RECORDING_TRACE
    else process.env.RSTREAM_QUALIFICATION_RECORDING_TRACE = previous
    await rm(directory, { recursive: true, force: true })
  }
})
