import assert from "node:assert/strict"
import { setTimeout as delay } from "node:timers/promises"
import { mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  beginFormatObservation,
  finishFormatObservation,
  validateFormatObservation,
} from "./source-formats.mjs"

// Exercises the actual browser -> platform -> authenticated tunnel -> GStreamer
// control path while two viewers share the same MediaMTX source session.
export async function qualifyQualityControls({
  context,
  page,
  platform,
  waitForVideo,
  observeSustainedPlayback,
  outputDirectory,
  direct = false,
}) {
  const sourceFormats = process.env.RSTREAM_QUALIFICATION_SOURCE_FORMATS === "1"
  const pathLabel = direct
    ? "Distribution path: Direct (MediaMTX fallback)"
    : "Distribution path: MediaMTX"
  const delivery = direct ? "direct" : "mediamtx"
  const inventory = await context.request.get(
    new URL("/api/devices", platform).href,
  )
  assert.equal(inventory.status(), 200)
  const { devices } = await inventory.json()
  assert.equal(devices.length, 1)
  const endpoint = new URL(`/api/devices/${devices[0].id}/quality`, platform)
    .href
  // The local harness exposes this listener only on loopback. Measure actual
  // encoded media reaching MediaMTX, independently of the producer's target.
  const mediaCounter = async () => {
    const response = await fetch("http://127.0.0.1:9998/metrics", {
      signal: AbortSignal.timeout(3000),
    })
    assert.equal(response.status, 200)
    const prefix = `paths_inbound_bytes{name="devices/${devices[0].id}",state="ready"} `
    const line = (await response.text())
      .split("\n")
      .find((line) => line.startsWith(prefix))
    assert.ok(line, "MediaMTX source byte counter must be present")
    const bytes = Number(line.slice(prefix.length))
    assert.ok(Number.isFinite(bytes) && bytes >= 0)
    return { bytes, at: performance.now() }
  }
  const read = async () => {
    const response = await context.request.get(endpoint, { timeout: 10_000 })
    assert.equal(response.status(), 200)
    return response.json()
  }
  const until = async (predicate) => {
    const deadline = Date.now() + 30_000
    let state
    while (Date.now() < deadline) {
      state = await read()
      if (predicate(state)) return state
      await delay(250)
    }
    throw new Error(`Quality convergence deadline: ${JSON.stringify(state)}`)
  }
  const select = page.getByLabel("Source quality", { exact: true })
  await select.waitFor({ state: "visible", timeout: 30_000 })
  const initial = await read()
  assert.deepEqual(
    initial.modes.map((mode) => mode.id),
    ["auto", "low", "medium", "high"],
  )
  assert.equal(initial.selected, "auto")
  if (sourceFormats) {
    assert.equal(
      initial.sourceFormat?.adaptive,
      false,
      "Manual format qualification requires the automatic ladder disabled",
    )
    assert.deepEqual(
      initial.sourceFormat.profiles.map(({ id, width, height, frameRate }) => ({
        id,
        width,
        height,
        frameRate,
      })),
      [
        {
          id: "small",
          width: 640,
          height: 360,
          frameRate: { numerator: 15, denominator: 1 },
        },
        {
          id: "medium",
          width: 960,
          height: 540,
          frameRate: { numerator: 24, denominator: 1 },
        },
        {
          id: "large",
          width: 1280,
          height: 720,
          frameRate: { numerator: 30, denominator: 1 },
        },
      ],
    )
  }
  const other = direct ? null : await context.newPage()
  const viewers = other ? [page, other] : [page]
  const measurements = []
  const failures = []
  other?.on("pageerror", (error) => failures.push(error.message))
  try {
    await other?.goto(platform, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    })
    await other
      ?.getByText("Distribution path: MediaMTX", { exact: true })
      .waitFor({ timeout: 60_000 })
    if (other) await waitForVideo(other, 30_000)
    const sequence = sourceFormats
      ? [
          ["low", 1000],
          ["medium", 3000],
          ["high", 6000],
          ["low", 1000],
          ["auto", 6000],
          ["medium", 3000],
          ["low", 1000],
          ["high", 6000],
          ["auto", 6000],
        ]
      : [
          ["low", 1000],
          ["medium", 4000],
          ["high", 10000],
          ["auto", 10000],
        ]
    for (const [mode, ceiling] of sequence) {
      const requested = sourceFormats
        ? initial.sourceFormat.profiles.find(
            (profile) =>
              profile.id ===
              (initial.modes.find((candidate) => candidate.id === mode)
                ?.sourceProfile ?? initial.sourceFormat.defaultProfile),
          )
        : null
      if (sourceFormats) {
        assert.ok(requested)
        await Promise.all(
          viewers.map((viewer) => beginFormatObservation(viewer, requested)),
        )
      }
      await select.selectOption(mode, { timeout: 30_000 })
      const state = await until(
        (state) =>
          state.selected === mode &&
          state.activeEncoders === 1 &&
          state.maxAppliedBitrateKbps <= ceiling &&
          state.minAppliedBitrateKbps >= 500 &&
          (!requested ||
            (state.sourceFormat?.pendingEncoders === 0 &&
              state.sourceFormat.profiles.some(
                (profile) =>
                  profile.id === requested.id && profile.observedEncoders === 1,
              ))),
      )
      assert.equal(state.failedUpdates, 0)
      if (sourceFormats) assert.equal(state.sourceFormat.failedUpdates, 0)
      await other?.waitForFunction(
        (mode) =>
          document.querySelector('select[aria-label="Source quality"]')
            ?.value === mode,
        mode,
        { timeout: 15_000 },
      )
      // Allow queued pre-change packets to drain before the rate observation.
      await delay(2000)
      const before = direct ? null : await mediaCounter()
      const playback = await Promise.all(
        viewers.map((viewer) =>
          observeSustainedPlayback(viewer, {
            durationMilliseconds: 5000,
            label: pathLabel,
            ...(requested
              ? {
                  minimumFramesPerSecond:
                    (0.8 * requested.frameRate.numerator) /
                    requested.frameRate.denominator,
                }
              : {}),
          }),
        ),
      )
      const after = direct ? null : await mediaCounter()
      const receivedKbps =
        before && after
          ? ((after.bytes - before.bytes) * 8) / (after.at - before.at)
          : null
      if (!direct)
        assert.ok(
          receivedKbps > 0 && receivedKbps <= ceiling * 1.5,
          `Received ${receivedKbps.toFixed(0)} kbps for ${mode} ceiling ${ceiling}`,
        )
      const transitions = sourceFormats
        ? await Promise.all(viewers.map(finishFormatObservation))
        : undefined
      const measurement = {
        mode,
        ceiling,
        applied: state.maxAppliedBitrateKbps,
        receivedKbps,
        framesPerSecond: playback.map((value) => value.framesPerSecond),
        ...(requested ? { requested, transitions } : {}),
      }
      measurements.push(measurement)
      // Retain failed transition observations too, before asserting their gates.
      if (sourceFormats && outputDirectory)
        await writeFile(
          join(outputDirectory, `source-format-transitions-${delivery}.json`),
          JSON.stringify(measurements, null, 2) + "\n",
        )
      if (requested) {
        for (const observation of playback) {
          assert.equal(observation.width, requested.width)
          assert.equal(observation.height, requested.height)
        }
        transitions.forEach(validateFormatObservation)
        if (
          measurements.length === 1 &&
          process.env.RSTREAM_UI_CAPTURE_DIRECTORY
        ) {
          const directory = process.env.RSTREAM_UI_CAPTURE_DIRECTORY
          await mkdir(directory, { recursive: true })
          for (const [suffix, viewport] of [
            ["desktop", { width: 1440, height: 900 }],
            ["mobile", { width: 390, height: 844 }],
          ]) {
            await page.setViewportSize(viewport)
            await page.screenshot({
              path: join(
                directory,
                `source-formats-${delivery}-inline-${suffix}.png`,
              ),
              fullPage: true,
            })
            await page
              .getByRole("button", { name: "Full page", exact: true })
              .click()
            await page.screenshot({
              path: join(
                directory,
                `source-formats-${delivery}-full-page-${suffix}.png`,
              ),
            })
            await page
              .getByRole("button", { name: "Exit full page", exact: true })
              .click()
          }
          await page.setViewportSize({ width: 1440, height: 900 })
        }
      }
    }
    if (!direct)
      assert.ok(
        measurements[2].receivedKbps > measurements[0].receivedKbps * 2,
        "High must produce measurably more encoded media than low on the uncongested test path",
      )
    // Delayed concurrent writes must not silently replace another viewer's choice.
    const stale = await context.request.put(endpoint, {
      headers: { Origin: new URL(platform).origin },
      data: { mode: "low", version: initial.version },
    })
    assert.equal(stale.status(), 409)
    assert.equal((await read()).selected, "auto")
    assert.deepEqual(failures, [])
    return {
      measurements,
      viewers: viewers.length,
      activeEncoders: 1,
      staleSelectionRejected: true,
    }
  } catch (error) {
    if (sourceFormats && outputDirectory) {
      const observations = await Promise.allSettled(
        viewers.map(finishFormatObservation),
      )
      await writeFile(
        join(outputDirectory, `source-format-failure-${delivery}.json`),
        JSON.stringify(
          {
            measurements,
            observations: observations
              .filter((result) => result.status === "fulfilled")
              .map((result) => result.value),
          },
          null,
          2,
        ) + "\n",
      )
    }
    throw error
  } finally {
    await other?.close()
  }
}
