import assert from "node:assert/strict"
import { execFile } from "node:child_process"
import { mkdtemp, readFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { promisify } from "node:util"

// Real decoded MP4, with controlled index gaps/expiry/outages. MediaMTX's own
// recording, authorization and retention are qualified by the separate native
// integration test. This fixture exercises the actual application controls.
export async function recordingFixture(page) {
  const directory = await mkdtemp(join(tmpdir(), "rstream-recording-ui-"))
  const clips = new Map()
  const requests = []
  const failures = []
  const end = Math.floor(Date.now() / 1000) * 1000
  const iso = (offset) => new Date(end + offset).toISOString()
  let indexMode = "available",
    clipMode = "available",
    release
  const media = (seconds) => {
    if (!clips.has(seconds))
      clips.set(
        seconds,
        (async () => {
          const path = join(directory, `${seconds}.mp4`)
          await promisify(execFile)(
            "ffmpeg",
            [
              "-v",
              "error",
              "-nostdin",
              "-f",
              "lavfi",
              "-i",
              "testsrc2=size=640x360:rate=15",
              "-t",
              String(seconds),
              "-an",
              "-c:v",
              "libx264",
              "-preset",
              "ultrafast",
              "-pix_fmt",
              "yuv420p",
              "-profile:v",
              "baseline",
              "-level:v",
              "3.1",
              "-g",
              "15",
              "-movflags",
              "+faststart",
              path,
            ],
            { timeout: 15000 },
          )
          return readFile(path)
        })(),
      )
    return clips.get(seconds)
  }
  await page.route("**/api/devices/*/recordings", (route) => {
    if (indexMode === "disabled") return route.fulfill({ status: 204 })
    if (indexMode === "unavailable")
      return route.fulfill({ status: 503, json: { error: "Unavailable" } })
    return route.fulfill({
      json: {
        windowStart: iso(-300000),
        windowEnd: iso(0),
        maximumClipSeconds: 6,
        spans:
          indexMode === "empty"
            ? []
            : [
                { start: iso(-280000), end: iso(-160000) },
                { start: iso(-90000), end: iso(-5000) },
              ],
      },
    })
  })
  await page.route("**/api/devices/*/recordings/playback?*", async (route) => {
    const url = new URL(route.request().url())
    const seconds = Number(url.searchParams.get("duration"))
    requests.push(url)
    try {
      assert.ok(seconds > 0 && seconds <= 6)
      assert.equal(url.username, "")
      assert.deepEqual([...url.searchParams.keys()], ["start", "duration"])
      if (clipMode === "expired") return await route.fulfill({ status: 404 })
      if (clipMode === "delayed")
        await new Promise((resolve) => {
          release = resolve
        })
      const body = await media(seconds)
      await route.fulfill({
        headers: {
          "Content-Type": "video/mp4",
          "Cache-Control": "private, no-store",
          "Accept-Ranges": "none",
        },
        body,
      })
    } catch (error) {
      failures.push(error)
      await route.abort().catch(() => {})
    }
  })
  const recorded = () => page.getByLabel("Recorded video", { exact: true })
  const playing = async () => {
    await page.waitForFunction(() => {
      const video = document.querySelector('video[aria-label="Recorded video"]')
      return video?.readyState >= 2 && !video.paused && video.currentTime > 0
    })
  }
  const seek = async (offset) => {
    await page
      .getByRole("slider", { name: "Recording time" })
      .evaluate((element, at) => {
        // Use the native range setter and normal input/pointer events, so React's
        // own draft/debounce/commit path runs rather than calling a hook directly.
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        ).set.call(element, String(at))
        element.dispatchEvent(new Event("input", { bubbles: true }))
        element.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }))
      }, end + offset)
  }
  return {
    async qualify({ video, source, capture }) {
      await page
        .getByRole("button", { name: "Recent recordings", exact: true })
        .click()
      await playing()
      assert.equal(await video.evaluate((element) => element.paused), true)
      assert.equal(
        await video.evaluate(
          (element, stream) => element.srcObject === stream,
          source,
        ),
        true,
      )
      await page
        .getByRole("button", { name: "Pause recording", exact: true })
        .click()
      assert.equal(await recorded().evaluate((element) => element.paused), true)
      await page.setViewportSize({ width: 1440, height: 900 })
      await capture("inline-recording-desktop")
      await page.getByRole("button", { name: "Full page", exact: true }).click()
      await capture("full-page-recording-desktop")
      await page.keyboard.press("Escape")
      await page.setViewportSize({ width: 390, height: 844 })
      await capture("inline-recording-mobile")
      await page.getByRole("button", { name: "Full page", exact: true }).click()
      await capture("full-page-recording-mobile")
      for (const size of [
        { width: 320, height: 568 },
        { width: 844, height: 390 },
      ]) {
        await page.setViewportSize(size)
        const play = await page
          .getByRole("button", { name: "Play recording", exact: true })
          .boundingBox()
        const live = await page
          .getByRole("button", { name: "Return to live", exact: true })
          .boundingBox()
        assert.ok(
          Math.abs(play.y - live.y) < 1,
          "Mobile replay controls stay on one line",
        )
        assert.equal(
          await page
            .getByRole("dialog")
            .evaluate((element) => element.scrollWidth <= element.clientWidth),
          true,
        )
      }
      await capture("full-page-recording-landscape")
      await page.keyboard.press("Escape")
      await page.setViewportSize({ width: 390, height: 844 })

      const beforeGap = requests.length
      await seek(-125000)
      await page
        .getByText(
          "No recording at this time. Choose another point or return to live.",
          { exact: true },
        )
        .waitFor()
      assert.equal(
        requests.length,
        beforeGap,
        "A gap never silently starts a different recording",
      )
      await capture("inline-recording-gap")

      await seek(-161000)
      await page
        .getByText(
          "End of this recording. Choose another point or return to live.",
          { exact: true },
        )
        .waitFor()
      assert.equal(
        requests.length,
        beforeGap + 1,
        "Reaching a gap never skips to the next span",
      )
      const continuation = page.waitForRequest((request) => {
        const url = new URL(request.url())
        return (
          url.pathname.endsWith("/recordings/playback") &&
          url.searchParams.get("start") === iso(-49000)
        )
      })
      await seek(-55000)
      await continuation
      await playing()
      await page
        .getByRole("button", { name: "Pause recording", exact: true })
        .click()
      await page.evaluate(() => {
        Object.defineProperty(document, "hidden", {
          configurable: true,
          value: true,
        })
        document.dispatchEvent(new Event("visibilitychange"))
      })
      await page.waitForFunction(
        () =>
          !document
            .querySelector('video[aria-label="Recorded video"]')
            .getAttribute("src"),
      )
      await page.evaluate(() => {
        delete document.hidden
        document.dispatchEvent(new Event("visibilitychange"))
      })
      await page.waitForFunction(() => {
        const video = document.querySelector(
          'video[aria-label="Recorded video"]',
        )
        return video.readyState >= 2 && video.paused
      })
      const slider = page.getByRole("slider", { name: "Recording time" })
      const beforeKeyboard = requests.length
      await slider.focus()
      await slider.press("ArrowRight")
      await playing()
      assert.ok(
        requests.length > beforeKeyboard,
        "Keyboard seek loads a bounded clip",
      )
      await page
        .getByRole("button", { name: "Pause recording", exact: true })
        .click()

      clipMode = "expired"
      await seek(-40000)
      await page
        .getByText(
          "This recording could not be loaded or has expired. Choose another point.",
          { exact: true },
        )
        .waitFor()
      await capture("inline-recording-expired")
      clipMode = "available"
      await seek(-35000)
      await playing()
      await page
        .getByRole("button", { name: "Pause recording", exact: true })
        .click()

      indexMode = "unavailable"
      await page
        .getByText("Recording list unavailable.", { exact: true })
        .waitFor({ timeout: 10000 })
      assert.equal(await slider.isDisabled(), true)
      await page
        .getByRole("button", { name: "Return to live", exact: true })
        .click()
      await page
        .getByRole("button", { name: "Recent recordings", exact: true })
        .click()
      await page
        .getByText(
          "Recent recordings are unavailable. Try another point after the list recovers.",
          { exact: true },
        )
        .waitFor()
      indexMode = "available"
      await page.waitForFunction(
        () =>
          !document.querySelector('input[aria-label="Recording time"]')
            .disabled,
        null,
        { timeout: 10000 },
      )

      clipMode = "delayed"
      const delayedRequest = page.waitForRequest((request) =>
        request.url().includes("/recordings/playback?"),
      )
      await seek(-30000)
      await delayedRequest
      // Returning live must invalidate even a response already being generated.
      await page
        .getByRole("button", { name: "Return to live", exact: true })
        .click()
      release?.()
      clipMode = "available"
      await page.waitForFunction(
        () => !document.querySelector('video[aria-label="Live video"]').paused,
      )
      assert.equal(await recorded().count(), 0)
      assert.equal(
        await video.evaluate(
          (element, stream) => element.srcObject === stream,
          source,
        ),
        true,
      )

      indexMode = "empty"
      await page.waitForResponse((response) =>
        /\/recordings$/.test(response.url()),
      )
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve)),
          ),
      )
      await page
        .getByRole("button", { name: "Recent recordings", exact: true })
        .click()
      await page
        .getByText(
          "No recent recordings yet. Recordings appear while the source is streaming.",
          { exact: true },
        )
        .waitFor()
      await capture("inline-recording-empty")
      indexMode = "disabled"
      await page
        .getByRole("button", { name: "Return to live", exact: true })
        .waitFor({ state: "hidden", timeout: 10000 })
      assert.equal(
        await page
          .getByRole("button", { name: "Recent recordings", exact: true })
          .count(),
        0,
      )
      assert.deepEqual(failures, [])
      console.log(
        "PASS: recorded MP4 controls, timeline gaps, expiry, index outage/recovery, keyboard seek, mobile layouts and live-session preservation",
      )
    },
    async close() {
      release?.()
      await Promise.allSettled([...clips.values()])
      await rm(directory, { recursive: true, force: true })
    },
  }
}
