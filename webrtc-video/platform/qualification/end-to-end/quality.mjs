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
      // Allow queued pre-change packets to drain before the rate observation.
      await delay(2000)
      const before = await mediaCounter()
      const playback = await Promise.all(
        [page, other].map((viewer) =>
          observeSustainedPlayback(viewer, {
            durationMilliseconds: 5000,
            label: "Distribution path: MediaMTX",
          }),
        ),
      )
      const after = await mediaCounter()
      const receivedKbps =
        ((after.bytes - before.bytes) * 8) / (after.at - before.at)
      assert.ok(
        receivedKbps > 0 && receivedKbps <= ceiling * 1.5,
        `Received ${receivedKbps.toFixed(0)} kbps for ${mode} ceiling ${ceiling}`,
      )
      measurements.push({
        mode,
        ceiling,
        applied: state.maxAppliedBitrateKbps,
        receivedKbps,
        framesPerSecond: playback.map((value) => value.framesPerSecond),
      })
    }
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
      viewers: 2,
      activeEncoders: 1,
      staleSelectionRejected: true,
    }
  } finally {
    await other.close()
  }
}
