import assert from "node:assert/strict"
import { execFile } from "node:child_process"
import { writeFile } from "node:fs/promises"
import { join } from "node:path"
import { setTimeout as delay } from "node:timers/promises"
import { promisify } from "node:util"
import {
  beginFormatObservation,
  finishFormatObservation,
} from "./source-formats.mjs"

const exec = promisify(execFile)
const filler = "/recordings/.qualification-fill"

// Fault injection is restricted to the disposable local helper's exact tmpfs.
// Never fill an arbitrary path, host filesystem or persistent recording volume.
export async function qualifyRecording({
  context,
  page,
  platform,
  container,
  waitForVideo,
  outputDirectory,
  mark,
}) {
  assert.match(container, /^rstream-video-mediamtx-[a-zA-Z0-9-]+$/)
  const docker = async (...args) =>
    exec("docker", args, { timeout: 15000, maxBuffer: 2 * 1024 * 1024 })
  const config = JSON.parse(
    (await docker("inspect", "--format", "{{json .HostConfig}}", container))
      .stdout,
  )
  assert.equal(
    config.Tmpfs?.["/recordings"],
    "rw,nosuid,nodev,noexec,size=512m,uid=10001,gid=10001,mode=0700",
  )
  assert.equal(config.ReadonlyRootfs, true)
  const result = { observations: [], passed: false }
  let other,
    filling = false
  const failures = []
  const read = async (path) => {
    const response = await context.request.get(new URL(path, platform).href, {
      timeout: 10000,
    })
    try {
      return { status: response.status(), body: await response.json() }
    } finally {
      await response.dispose()
    }
  }
  const until = async (check, label, timeout = 30000) => {
    const end = Date.now() + timeout
    while (Date.now() < end) {
      const value = await check()
      if (value) return value
      await delay(500)
    }
    throw new Error(`Recording qualification deadline: ${label}`)
  }
  const observe = async (phase, milliseconds, action = async () => {}) => {
    mark(`recording-${phase}`)
    await beginFormatObservation(page, { width: 1280, height: 720 })
    let sample
    try {
      await Promise.all([delay(milliseconds), action()])
    } finally {
      sample = await finishFormatObservation(page)
      result.observations.push({
        phase,
        ...sample,
        framesPerSecond: (1000 * sample.frames) / sample.durationMilliseconds,
      })
    }
    assert.ok(
      (sample.frames * 1000) / sample.durationMilliseconds >= 24,
      `${phase}: presented frame rate fell below 24 fps`,
    )
    assert.ok(
      sample.longestGapMilliseconds <= 500,
      `${phase}: presentation gap ${sample.longestGapMilliseconds}ms exceeded 500ms`,
    )
    assert.equal(sample.streamReplaced, false)
    assert.equal(sample.mediaTimeRegressions, 0)
    assert.equal(sample.rtpTimestampRegressions, 0)
    assert.ok(sample.rtpSamples > 1)
  }
  try {
    const inventory = await read("/api/devices")
    assert.equal(inventory.status, 200)
    assert.equal(inventory.body.devices.length, 1)
    const device = inventory.body.devices[0].id
    const base = `/api/devices/${device}`
    const index = async () => {
      const response = await read(`${base}/recordings`)
      assert.equal(
        response.status,
        200,
        "Real recording index must be readable",
      )
      return response.body
    }
    await until(
      async () => (await index()).spans.length > 0,
      "first recorded span",
    )
    other = await context.newPage()
    other.on("pageerror", (error) => failures.push(error.message))
    await other.goto(platform, {
      waitUntil: "domcontentloaded",
      timeout: 60000,
    })
    await other
      .getByText("Distribution path: MediaMTX", { exact: true })
      .waitFor({ timeout: 60000 })
    await waitForVideo(other, 30000)
    await page.bringToFront()
    const shared = async () => {
      const quality = await read(`${base}/quality`)
      assert.equal(quality.status, 200)
      assert.equal(quality.body.activeEncoders, 1)
      const metrics = await read(`${base}/metrics`)
      return (
        metrics.status === 200 &&
        metrics.body.state === "ready" &&
        metrics.body.readers === 2
      )
    }
    await until(shared, "two readers sharing one encoder")
    const live = await page
      .getByLabel("Live video", { exact: true })
      .elementHandle()
    const stream = await live.evaluateHandle((element) => element.srcObject)
    const playbackRequests = []
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === `${base}/recordings/playback`)
        playbackRequests.push(request)
    })
    mark("recording-replay-started")
    await page
      .getByRole("button", { name: "Recent recordings", exact: true })
      .click()
    await page.waitForFunction(
      () => {
        const video = document.querySelector(
          'video[aria-label="Recorded video"]',
        )
        return (
          video?.videoWidth === 1280 &&
          video.videoHeight === 720 &&
          video.currentTime >= 1
        )
      },
      null,
      { timeout: 30000 },
    )
    await page
      .getByRole("button", { name: "Pause recording", exact: true })
      .click()
    const request = playbackRequests[0]
    assert.ok(
      request,
      "Replay must retrieve actual MediaMTX bytes through the platform",
    )
    const response = await request.response()
    assert.equal(response.status(), 200)
    result.replay = {
      status: response.status(),
      width: 1280,
      height: 720,
      duration: Number(new URL(request.url()).searchParams.get("duration")),
    }
    assert.ok(result.replay.duration > 0 && result.replay.duration <= 30)
    // A paused native player can suspend download before EOF. Return live
    // first, then require the old media request to finish or cancel promptly.
    mark("recording-return-live-requested")
    await page.evaluate(() => {
      window.__rstreamQualificationEvents.push({
        name: "recording-return-live-requested",
        at: Date.now(),
        url: document.querySelector('video[aria-label="Recorded video"]')
          .currentSrc,
      })
    })
    await page
      .getByRole("button", { name: "Return to live", exact: true })
      .click()
    let deadline
    try {
      const finished = await Promise.race([
        response.finished(),
        new Promise((_, reject) => {
          deadline = setTimeout(
            () =>
              reject(
                new Error(
                  "Old recording request did not finish after returning live",
                ),
              ),
            5000,
          )
        }),
      ])
      if (finished) assert.match(finished.message, /ERR_ABORTED/)
      result.replay.requestReleased = true
    } finally {
      clearTimeout(deadline)
    }
    await page.waitForFunction(
      () => !document.querySelector('video[aria-label="Live video"]').paused,
    )
    assert.equal(
      await live.evaluate(
        (element, stream) => element.srcObject === stream,
        stream,
      ),
      true,
    )
    await observe("baseline", 10000)
    const faultSince = new Date().toISOString()
    const storage = async () => {
      const output = (
        await docker("exec", container, "df", "-k", "/recordings")
      ).stdout
        .trim()
        .split("\n")
        .at(-1)
        .trim()
        .split(/\s+/)
      return {
        totalKiB: Number(output[1]),
        usedKiB: Number(output[2]),
        availableKiB: Number(output[3]),
      }
    }
    result.before = await storage()
    await observe("storage-full", 20000, async () => {
      filling = true
      let error
      try {
        await docker(
          "exec",
          container,
          "dd",
          "if=/dev/zero",
          `of=${filler}`,
          "bs=1048576",
          "count=513",
        )
      } catch (cause) {
        error = cause
      }
      assert.equal(
        error?.code,
        1,
        "The bounded tmpfs must actually reject writes",
      )
      assert.match(error.stderr, /No space left on device/)
      result.full = await storage()
      assert.equal(result.full.availableKiB, 0)
      await until(
        async () => {
          const log = await docker("logs", "--since", faultSince, container)
          return /\[recorder\].*no space left on device/i.test(
            log.stdout + log.stderr,
          )
        },
        "MediaMTX recorder reports ENOSPC",
        10000,
      )
    })
    const log = await docker("logs", "--since", faultSince, container)
    result.storageErrors = (log.stdout + log.stderr)
      .split("\n")
      .filter((line) =>
        /\[recorder\].*no space left on device/i.test(line),
      ).length
    assert.ok(
      result.storageErrors >= 1 && result.storageErrors <= 10,
      "Recorder failures must be reported without a tight retry loop",
    )
    assert.equal(await shared(), true)
    assert.equal(
      (
        await docker(
          "inspect",
          "--format",
          "{{.State.Running}} {{.State.OOMKilled}}",
          container,
        )
      ).stdout.trim(),
      "true false",
    )
    await docker("exec", container, "rm", filler)
    filling = false
    const releasedAt = Date.now()
    mark("recording-storage-recovering")
    const recovered = await until(async () => {
      const value = await index()
      return (
        value.spans.some((span) => Date.parse(span.end) > releasedAt + 1000) &&
        value
      )
    }, "new recording after space is released")
    result.recoveredSpans = recovered.spans
    result.recoveryMilliseconds = Date.now() - releasedAt
    result.after = await storage()
    assert.ok(result.after.availableKiB > 0)
    await observe("recovered", 10000)
    assert.equal(await shared(), true)
    assert.equal(
      await live.evaluate(
        (element, stream) => element.srcObject === stream,
        stream,
      ),
      true,
    )
    assert.deepEqual(failures, [])
    result.passed = true
    return result
  } finally {
    if (filling)
      await docker("exec", container, "rm", "-f", filler).catch(() => {})
    await other?.close()
    await writeFile(
      join(outputDirectory, "recording.json"),
      JSON.stringify(result, null, 2) + "\n",
      { mode: 0o600 },
    )
  }
}
