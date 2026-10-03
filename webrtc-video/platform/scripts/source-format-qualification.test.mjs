import assert from "node:assert/strict"
import test from "node:test"
import { validateFormatObservation } from "../qualification/end-to-end/source-formats.mjs"

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
