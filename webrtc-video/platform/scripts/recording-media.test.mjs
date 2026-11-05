import assert from "node:assert/strict"
import test from "node:test"
import { readRecordingMedia } from "../src/lib/recording-media.ts"

const media = (body, headers = {}) =>
  new Response(body, {
    headers: { "Content-Type": "video/mp4", ...headers },
  })
const signal = () => new AbortController().signal

test("a chunked clip becomes a seekable Blob containing exactly its bytes", async () => {
  const blob = await readRecordingMedia(
    media(new Uint8Array([1, 2, 3])),
    signal(),
  )
  assert.equal(blob.type, "video/mp4")
  assert.deepEqual([...new Uint8Array(await blob.arrayBuffer())], [1, 2, 3])
})

test("rejects invalid, empty, oversized or incomplete clips", async () => {
  for (const response of [
    new Response("private upstream details", { status: 403 }),
    media("bad", { "Content-Type": "text/html" }),
    media("", {}),
    media("mp4", { "Content-Length": "4" }),
    media("mp4", { "Content-Length": "invalid" }),
    media("mp4", { "Content-Length": String(64 * 1024 * 1024 + 1) }),
  ])
    await assert.rejects(readRecordingMedia(response, signal()))
  let cancelled = false
  const oversized = media(
    new ReadableStream(
      {
        pull(c) {
          c.enqueue(new Uint8Array(64 * 1024 * 1024 + 1))
        },
        cancel() {
          cancelled = true
        },
      },
      { highWaterMark: 0 },
    ),
  )
  await assert.rejects(readRecordingMedia(oversized, signal()))
  assert.equal(cancelled, true)
})

test("cancels a stalled body when seeking away; partial bytes never become media", async () => {
  let cancelled = false
  const controller = new AbortController()
  const response = media(
    new ReadableStream({
      start(c) {
        c.enqueue(new Uint8Array([1]))
      },
      cancel() {
        cancelled = true
      },
    }),
  )
  const pending = readRecordingMedia(response, controller.signal)
  const rejected = assert.rejects(pending, { name: "AbortError" })
  controller.abort()
  await rejected
  assert.equal(cancelled, true)
})
