import assert from "node:assert/strict"
import test from "node:test"
import { createVideoFrameCounter } from "../src/lib/video-frame-counter.ts"
import { PlaybackHealthTracker } from "../src/lib/video-playback-health.ts"

test("presented frames override Safari's zero quality counter and release the callback", () => {
  let callback,
    next = 0
  const cancelled = []
  const counter = createVideoFrameCounter({
    requestVideoFrameCallback(fn) {
      callback = fn
      return ++next
    },
    cancelVideoFrameCallback(handle) {
      cancelled.push(handle)
    },
    getVideoPlaybackQuality: () => ({ totalVideoFrames: 0 }),
  })
  assert.equal(counter.read(), null)
  const health = new PlaybackHealthTracker()
  for (let seconds = 0; seconds <= 30; seconds++) {
    callback(seconds * 1000, { presentedFrames: seconds * 30 + 1 })
    assert.equal(
      health.observe({
        active: true,
        decodedFrames: counter.read(),
        timeMilliseconds: seconds * 1000,
      }),
      false,
    )
  }
  const pending = next
  counter.stop()
  counter.stop()
  callback(31000, { presentedFrames: 9999 })
  assert.deepEqual(cancelled, [pending])
  assert.equal(next, pending)
  assert.equal(counter.read(), 901)
})

test("a presented-frame stall remains detectable", () => {
  let callback
  const counter = createVideoFrameCounter({
    requestVideoFrameCallback(fn) {
      callback = fn
      return 1
    },
    cancelVideoFrameCallback() {},
  })
  callback(0, { presentedFrames: 20 })
  const health = new PlaybackHealthTracker({
    startupGraceMilliseconds: 0,
    unhealthyMilliseconds: 2000,
  })
  assert.equal(
    health.observe({
      active: true,
      decodedFrames: counter.read(),
      timeMilliseconds: 0,
    }),
    false,
  )
  assert.equal(
    health.observe({
      active: true,
      decodedFrames: counter.read(),
      timeMilliseconds: 1000,
    }),
    false,
  )
  assert.equal(
    health.observe({
      active: true,
      decodedFrames: counter.read(),
      timeMilliseconds: 3000,
    }),
    true,
  )
  counter.stop()
})

test("older browsers use available counters without inventing missing measurements", () => {
  assert.equal(
    createVideoFrameCounter({
      getVideoPlaybackQuality: () => ({ totalVideoFrames: 42 }),
    }).read(),
    42,
  )
  assert.equal(
    createVideoFrameCounter({ webkitDecodedFrameCount: 23 }).read(),
    23,
  )
  assert.equal(createVideoFrameCounter({}).read(), null)
  assert.equal(
    createVideoFrameCounter({
      getVideoPlaybackQuality: () => {
        throw new Error("Unavailable")
      },
    }).read(),
    null,
  )
})
