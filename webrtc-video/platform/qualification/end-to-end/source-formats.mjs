import assert from "node:assert/strict"

// Observe real decoded frames, not requested encoder settings. Retain only
// aggregates and timestamp deltas, never SDP or credentials.
export async function beginFormatObservation(page, expected) {
  await page.locator("video").evaluate((video, expected) => {
    if (!video.requestVideoFrameCallback)
      throw new Error("Source-format qualification requires frame callbacks")
    window.__sourceFormatObservation?.stop()
    const stream = video.srcObject
    const started = performance.now()
    let previousAt = started
    let previousMediaTime = null
    let previousRTP = null
    const state = {
      expected,
      frames: 0,
      firstTargetMilliseconds: null,
      longestGapMilliseconds: 0,
      mediaTimeRegressions: 0,
      rtpTimestampRegressions: 0,
      rtpSamples: 0,
      streamReplaced: false,
    }
    let callback
    const observe = (now, metadata) => {
      state.frames++
      state.longestGapMilliseconds = Math.max(
        state.longestGapMilliseconds,
        now - previousAt,
      )
      state.streamReplaced ||=
        video.srcObject !== stream || document.querySelector("video") !== video
      if (previousMediaTime !== null && metadata.mediaTime < previousMediaTime)
        state.mediaTimeRegressions++
      if (Number.isInteger(metadata.rtpTimestamp)) {
        const timestamp = metadata.rtpTimestamp >>> 0
        if (
          previousRTP !== null &&
          (timestamp - previousRTP) >>> 0 >= 0x80000000
        )
          state.rtpTimestampRegressions++
        previousRTP = timestamp
        state.rtpSamples++
      }
      if (
        state.firstTargetMilliseconds === null &&
        video.videoWidth === expected.width &&
        video.videoHeight === expected.height
      )
        state.firstTargetMilliseconds = now - started
      previousAt = now
      previousMediaTime = metadata.mediaTime
      callback = video.requestVideoFrameCallback(observe)
    }
    callback = video.requestVideoFrameCallback(observe)
    window.__sourceFormatObservation = {
      state,
      stop: () => video.cancelVideoFrameCallback(callback),
    }
  }, expected)
}

export async function finishFormatObservation(page) {
  return page.evaluate(() => {
    const observation = window.__sourceFormatObservation
    if (!observation)
      throw new Error("Source format observation was not started")
    observation.stop()
    delete window.__sourceFormatObservation
    return observation.state
  })
}

export function validateFormatObservation(value) {
  assert.ok(value.frames > 0, "Transition must produce presented frames")
  assert.ok(value.rtpSamples > 1, "RTP timestamps must actually be observed")
  assert.equal(
    value.streamReplaced,
    false,
    "Format changes must preserve the video element and MediaStream",
  )
  assert.equal(
    value.mediaTimeRegressions,
    0,
    "Decoded media time must not move backwards",
  )
  assert.equal(
    value.rtpTimestampRegressions,
    0,
    "RTP timestamps must not move backwards",
  )
  assert.ok(
    value.firstTargetMilliseconds !== null &&
      value.firstTargetMilliseconds <= 10_000,
    `Requested dimensions were not presented within 10 seconds: ${value.firstTargetMilliseconds}`,
  )
  assert.ok(
    value.longestGapMilliseconds <= 3_000,
    `Format transition stalled presentation for ${value.longestGapMilliseconds} ms`,
  )
}
