import assert from "node:assert/strict"
import test from "node:test"

import {
  minimumUsableFramesPerSecond,
  PlaybackHealthTracker,
} from "../src/lib/video-playback-health.ts"

test("usable frame-rate thresholds scale with the negotiated source", () => {
  assert.equal(minimumUsableFramesPerSecond(30), 6)
  assert.equal(minimumUsableFramesPerSecond(1), 0.2)
  assert.equal(minimumUsableFramesPerSecond(undefined), 5)
  assert.equal(minimumUsableFramesPerSecond(Number.NaN), 5)
})

test("sustained low frame rate is unhealthy after grace and observation windows", () => {
  const tracker = new PlaybackHealthTracker({
    minimumFramesPerSecond: 5,
    startupGraceMilliseconds: 2000,
    unhealthyMilliseconds: 3000,
  })
  assert.equal(observe(tracker, 0, 0), false)
  assert.equal(observe(tracker, 1000, 2), false)
  assert.equal(observe(tracker, 2000, 4), false)
  assert.equal(observe(tracker, 3000, 6), false)
  assert.equal(observe(tracker, 4000, 8), false)
  assert.equal(observe(tracker, 5000, 10), true)
})

test("healthy playback clears a partial unhealthy window", () => {
  const tracker = new PlaybackHealthTracker({
    minimumFramesPerSecond: 5,
    startupGraceMilliseconds: 0,
    unhealthyMilliseconds: 2000,
  })
  assert.equal(observe(tracker, 0, 0), false)
  assert.equal(observe(tracker, 1000, 1), false)
  assert.equal(observe(tracker, 2000, 31), false)
  assert.equal(observe(tracker, 3000, 32), false)
  assert.equal(observe(tracker, 4000, 33), false)
  assert.equal(observe(tracker, 5000, 34), true)
})

test("inactive playback resets timing before monitoring resumes", () => {
  const tracker = new PlaybackHealthTracker({
    minimumFramesPerSecond: 5,
    startupGraceMilliseconds: 1000,
    unhealthyMilliseconds: 1000,
  })
  assert.equal(observe(tracker, 0, 0), false)
  assert.equal(observe(tracker, 1000, 1), false)
  assert.equal(
    tracker.observe({
      active: false,
      decodedFrames: 1,
      timeMilliseconds: 2000,
    }),
    false,
  )
  assert.equal(observe(tracker, 10000, 1), false)
  assert.equal(observe(tracker, 11000, 2), false)
  assert.equal(observe(tracker, 12000, 3), true)
})

test("counter and clock resets establish a fresh playback baseline", () => {
  const tracker = new PlaybackHealthTracker({
    minimumFramesPerSecond: 5,
    startupGraceMilliseconds: 0,
    unhealthyMilliseconds: 1000,
  })
  assert.equal(observe(tracker, 1000, 30), false)
  assert.equal(observe(tracker, 2000, 31), false)
  assert.equal(observe(tracker, 3000, 1), false)
  assert.equal(observe(tracker, 2500, 2), false)
  assert.equal(observe(tracker, 3500, 32), false)
})

test("playback health rejects invalid configuration and samples", () => {
  assert.throws(
    () => new PlaybackHealthTracker({ minimumFramesPerSecond: 0 }),
    /positive number/,
  )
  assert.throws(
    () => new PlaybackHealthTracker({ startupGraceMilliseconds: -1 }),
    /non-negative integer/,
  )
  assert.throws(
    () => new PlaybackHealthTracker({ unhealthyMilliseconds: 0 }),
    /positive integer/,
  )
  const tracker = new PlaybackHealthTracker()
  assert.throws(() => observe(tracker, Number.NaN, 0), /non-negative number/)
  assert.throws(() => observe(tracker, 0, -1), /non-negative number/)
})

function observe(tracker, timeMilliseconds, decodedFrames) {
  return tracker.observe({ active: true, decodedFrames, timeMilliseconds })
}
