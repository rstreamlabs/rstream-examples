import assert from "node:assert/strict"
import { setTimeout as delay } from "node:timers/promises"

// Exercises the actual browser -> platform -> authenticated tunnel -> GStreamer
// control path while two viewers share the same MediaMTX source session.
export async function qualifyQualityControls({
  context,
  page,
  platform,
  waitForVideo,
  observeSustainedPlayback,
}) {
  const inventory = await context.request.get(
    new URL("/api/devices", platform).href,
  )
  assert.equal(inventory.status(), 200)
  const { devices } = await inventory.json()
  assert.equal(devices.length, 1)
  const endpoint = new URL(`/api/devices/${devices[0].id}/quality`, platform)
    .href
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
  const other = await context.newPage()
  const failures = []
  other.on("pageerror", (error) => failures.push(error.message))
  try {
    await other.goto(platform, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    })
    await other
      .getByText("Distribution path: MediaMTX", { exact: true })
      .waitFor({ timeout: 60_000 })
    await waitForVideo(other, 30_000)
    const measurements = []
    for (const [mode, ceiling] of [
      ["low", 1000],
      ["medium", 4000],
      ["high", 10000],
      ["auto", 10000],
    ]) {
      await select.selectOption(mode, { timeout: 30_000 })
      const state = await until(
        (state) =>
          state.selected === mode &&
          state.activeEncoders === 1 &&
          state.maxAppliedBitrateKbps <= ceiling &&
          state.minAppliedBitrateKbps >= 500,
      )
      assert.equal(state.failedUpdates, 0)
      await other.waitForFunction(
        (mode) =>
          document.querySelector('select[aria-label="Source quality"]')
            ?.value === mode,
        mode,
        { timeout: 15_000 },
      )
      const playback = await Promise.all(
        [page, other].map((viewer) =>
          observeSustainedPlayback(viewer, {
            durationMilliseconds: 5000,
            label: "Distribution path: MediaMTX",
          }),
        ),
      )
      measurements.push({
        mode,
        ceiling,
        applied: state.maxAppliedBitrateKbps,
        framesPerSecond: playback.map((value) => value.framesPerSecond),
      })
    }
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
      viewers: 2,
      activeEncoders: 1,
      staleSelectionRejected: true,
    }
  } finally {
    await other.close()
  }
}
