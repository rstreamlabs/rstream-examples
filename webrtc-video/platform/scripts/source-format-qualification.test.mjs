import assert from "node:assert/strict"
import test from "node:test"
import {
  beginFormatObservation,
  finishFormatObservation,
  validateFormatObservation,
} from "../qualification/end-to-end/source-formats.mjs"

test("source-format qualification requires actual presentation and monotonic timestamps", () => {
  const valid = {
    frames: 200,
    rtpSamples: 200,
    streamReplaced: false,
    mediaTimeRegressions: 0,
    rtpTimestampRegressions: 0,
    firstTargetMilliseconds: 400,
    longestGapMilliseconds: 200,
  }
  validateFormatObservation(valid)
  for (const change of [
    { frames: 0 },
    { rtpSamples: 0 },
    { streamReplaced: true },
    { mediaTimeRegressions: 1 },
    { rtpTimestampRegressions: 1 },
    { firstTargetMilliseconds: null },
    { firstTargetMilliseconds: 10001 },
    { longestGapMilliseconds: 3001 },
  ])
    assert.throws(() => validateFormatObservation({ ...valid, ...change }))
})

test("frame observation handles RTP wrap and detects a final stall or removed video", async (t) => {
  const originalWindow = globalThis.window,
    originalDocument = globalThis.document
  t.after(() => {
    globalThis.window = originalWindow
    globalThis.document = originalDocument
  })
  let now = 0,
    callback
  t.mock.method(performance, "now", () => now)
  const video = {
    srcObject: {},
    videoWidth: 640,
    videoHeight: 360,
    requestVideoFrameCallback(fn) {
      callback = fn
      return 1
    },
    cancelVideoFrameCallback() {
      callback = null
    },
  }
  let currentVideo = video
  globalThis.window = {}
  globalThis.document = {
    querySelector() {
      return currentVideo
    },
  }
  const page = {
    locator() {
      return {
        evaluate(fn, arg) {
          return fn(video, arg)
        },
      }
    },
    evaluate(fn) {
      return fn()
    },
  }
  await beginFormatObservation(page, { width: 640, height: 360 })
  now = 100
  callback(now, { mediaTime: 1, rtpTimestamp: 0xfffffff0 })
  now = 200
  callback(now, { mediaTime: 2, rtpTimestamp: 2000 })
  now = 250
  const first = await finishFormatObservation(page)
  validateFormatObservation(first)
  assert.equal(first.longestGapMilliseconds, 100)
  assert.equal(callback, null)
  await beginFormatObservation(page, { width: 640, height: 360 })
  now = 300
  callback(now, { mediaTime: 3, rtpTimestamp: 5000 })
  currentVideo = {}
  now = 4000
  const stopped = await finishFormatObservation(page)
  assert.equal(stopped.streamReplaced, true)
  assert.equal(stopped.longestGapMilliseconds, 3700)
})
