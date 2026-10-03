import assert from "node:assert/strict"
import test from "node:test"
import { setTimeout as delay } from "node:timers/promises"
import {
  QualityClient,
  parseQualityState,
} from "../../shared/quality-client.ts"

const version = "a".repeat(32) + ":0"
const state = {
  modes: [
    { id: "auto", label: "Auto", bitrateKbps: 0 },
    { id: "low", label: "Low", bitrateKbps: 1000 },
  ],
  selected: "auto",
  version,
  activeEncoders: 1,
  minAppliedBitrateKbps: 5000,
  maxAppliedBitrateKbps: 5000,
  failedUpdates: 0,
}
async function eventually(check) {
  for (let i = 0; i < 100; i++) {
    if (check()) return
    await delay(5)
  }
  assert.fail("condition did not become true")
}

test("quality contract rejects malformed, unbounded and ambiguous capabilities", () => {
  assert.deepEqual(parseQualityState(state), state)
  for (const value of [
    { ...state, version: "old" },
    { ...state, modes: [state.modes[0], state.modes[0]] },
    { ...state, selected: "unknown" },
    { ...state, maxAppliedBitrateKbps: Infinity },
    { ...state, minAppliedBitrateKbps: 6000 },
  ])
    assert.throws(() => parseQualityState(value))
})

test("quality client discovers modes and sends source version on selection", async () => {
  const observed = [],
    calls = []
  let current = structuredClone(state)
  const client = new QualityClient({
    url: () => "https://platform.example/api/quality",
    onState: (value) => observed.push(value),
    onError: assert.fail,
    intervalMs: 60000,
    fetch: async (url, options) => {
      calls.push(options)
      assert.equal(options.redirect, "error")
      if (options.method === "PUT") {
        assert.deepEqual(JSON.parse(options.body), { mode: "low", version })
        current = {
          ...state,
          selected: "low",
          version: "a".repeat(32) + ":1",
          minAppliedBitrateKbps: 1000,
          maxAppliedBitrateKbps: 1000,
        }
      }
      return Response.json(current)
    },
  })
  client.start()
  try {
    await eventually(() => observed.length === 1)
    await client.select("low")
    await eventually(() => observed.at(-1).selected === "low")
    assert.equal(calls.filter((call) => call.method === "PUT").length, 1)
  } finally {
    client.stop()
  }
})

test("quality client aborts stalled bodies and never updates after stop", async () => {
  const errors = [],
    observed = []
  let body
  const client = new QualityClient({
    url: () => "https://device.example/api/quality",
    onState: (value) => observed.push(value),
    onError: (error) => errors.push(error),
    timeoutMs: 20,
    intervalMs: 60000,
    fetch: async (_url, options) =>
      new Response(
        new ReadableStream({
          start(controller) {
            body = controller
            options.signal.addEventListener(
              "abort",
              () => controller.error(options.signal.reason),
              { once: true },
            )
          },
        }),
      ),
  })
  client.start()
  await eventually(() => errors.length === 1)
  client.stop()
  assert.deepEqual(observed, [])
  assert.ok(body)
})

test("late responses from cancelled discovery cannot overwrite a new selection", async () => {
  let pending,
    requests = 0
  const observed = []
  const client = new QualityClient({
    url: () => "https://device.example/api/quality",
    onState: (value) => observed.push(value),
    onError: assert.fail,
    intervalMs: 10,
    fetch: async (_url, options) => {
      requests++
      if (requests === 2)
        return new Promise((resolve) => {
          pending = resolve
        })
      return Response.json(
        options.method === "PUT"
          ? { ...state, selected: "low", version: "a".repeat(32) + ":1" }
          : state,
      )
    },
  })
  client.start()
  try {
    await eventually(() => pending)
    await client.select("low")
    client.stop()
    const length = observed.length
    pending(Response.json(state))
    await delay(25)
    assert.equal(observed.length, length)
    assert.ok(observed.some((value) => value.selected === "low"))
  } finally {
    client.stop()
  }
})

test("quality client treats missing optional capabilities without an error", async () => {
  const observed = []
  const client = new QualityClient({
    url: () => "https://device.example/api/quality",
    onState: (value) => observed.push(value),
    onError: assert.fail,
    fetch: async () => new Response(null, { status: 404 }),
  })
  client.start()
  try {
    await eventually(() => observed.length === 1)
    assert.equal(observed[0], null)
  } finally {
    client.stop()
  }
})
