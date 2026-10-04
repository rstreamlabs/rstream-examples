import assert from "node:assert/strict"
import test from "node:test"
import { setTimeout as delay } from "node:timers/promises"
import {
  QualityClient,
  parseQualityState,
  sourceFormatDescription,
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

test("optional source formats preserve observed/requested state and reject inconsistent counts", () => {
  const formatState = {
    ...structuredClone(state),
    sourceFormat: {
      defaultProfile: "small",
      adaptive: false,
      activeEncoders: 2,
      pendingEncoders: 1,
      unconfirmedEncoders: 1,
      failedUpdates: 0,
      profiles: [
        {
          id: "small",
          width: 640,
          height: 360,
          frameRate: { numerator: 30000, denominator: 1001 },
          requestedEncoders: 2,
          observedEncoders: 1,
        },
      ],
    },
  }
  formatState.modes[1].sourceProfile = "small"
  assert.deepEqual(parseQualityState(formatState), formatState)
  for (const mutate of [
    (s) => {
      s.sourceFormat = null
    },
    (s) => {
      delete s.sourceFormat
    },
    (s) => {
      s.sourceFormat.profiles = []
    },
    (s) => {
      s.sourceFormat.profiles.push(s.sourceFormat.profiles[0])
    },
    (s) => {
      s.sourceFormat.profiles[0].width = 641
    },
    (s) => {
      s.sourceFormat.profiles[0].height = 16386
    },
    (s) => {
      s.sourceFormat.profiles[0].frameRate.denominator = 0
    },
    (s) => {
      s.sourceFormat.profiles[0].frameRate.numerator = 1_000_001
    },
    (s) => {
      s.sourceFormat.profiles[0].frameRate = { numerator: 241, denominator: 1 }
    },
    (s) => {
      s.sourceFormat.profiles[0].requestedEncoders = 3
    },
    (s) => {
      s.sourceFormat.profiles[0].observedEncoders = 2
    },
    (s) => {
      s.sourceFormat.unconfirmedEncoders = 0
    },
    (s) => {
      s.sourceFormat.pendingEncoders = 3
    },
    (s) => {
      s.sourceFormat.defaultProfile = "missing"
    },
    (s) => {
      s.sourceFormat.failedUpdates = Infinity
    },
    (s) => {
      s.sourceFormat.adaptive = "yes"
    },
    (s) => {
      s.modes[1].sourceProfile = "unknown"
    },
    (s) => {
      s.modes[0].sourceProfile = "small"
    },
  ]) {
    const invalid = structuredClone(formatState)
    mutate(invalid)
    assert.throws(() => parseQualityState(invalid))
  }
})

test("source format text uses confirmed caps rather than a newly selected profile", () => {
  assert.equal(sourceFormatDescription(state), null)
  const current = {
    ...state,
    selected: "high",
    sourceFormat: {
      activeEncoders: 1,
      pendingEncoders: 1,
      unconfirmedEncoders: 0,
      profiles: [
        {
          id: "small",
          width: 640,
          height: 360,
          frameRate: { numerator: 30000, denominator: 1001 },
          observedEncoders: 1,
          requestedEncoders: 0,
        },
        {
          id: "large",
          width: 1280,
          height: 720,
          frameRate: { numerator: 30, denominator: 1 },
          observedEncoders: 0,
          requestedEncoders: 1,
        },
      ],
    },
  }
  assert.equal(
    sourceFormatDescription(current),
    "Source format: 640 × 360 · 29.97 fps. Updating…",
  )
  current.sourceFormat.pendingEncoders = 0
  current.sourceFormat.profiles[0].observedEncoders = 0
  current.sourceFormat.unconfirmedEncoders = 1
  assert.equal(
    sourceFormatDescription(current),
    "Source format awaiting confirmation.",
  )
  current.sourceFormat.activeEncoders = 0
  assert.equal(
    sourceFormatDescription(current),
    "Source format applies when streaming starts.",
  )
  current.sourceFormat.activeEncoders = 2
  current.sourceFormat.profiles.forEach((profile) => {
    profile.observedEncoders = 1
  })
  current.sourceFormat.unconfirmedEncoders = 0
  assert.equal(
    sourceFormatDescription(current),
    "Source formats vary between active connections.",
  )
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

test("disabled quality completes an empty 204 stream without cancelling the request", async () => {
  const observed = []
  let cancelled = false
  let completed = false
  let closeTimer
  // Chromium can expose an empty stream for 204 instead of a null body.
  // Cancelling it before EOF produces a failed network request in DevTools.
  const body = new ReadableStream({
    start(controller) {
      closeTimer = setTimeout(() => {
        completed = true
        controller.close()
      }, 20)
    },
    cancel() {
      cancelled = true
      clearTimeout(closeTimer)
    },
  })
  const response = new Response(null, { status: 204 })
  Object.defineProperty(response, "body", { value: body })
  Object.defineProperty(response, "arrayBuffer", {
    value: () => new Response(body).arrayBuffer(),
  })
  const client = new QualityClient({
    url: () => "https://device.example/api/quality",
    onState: (value) => observed.push(value),
    onError: assert.fail,
    intervalMs: 60000,
    fetch: async () => response,
  })
  client.start()
  try {
    await eventually(() => observed.length === 1)
    assert.deepEqual(observed, [null])
    assert.equal(cancelled, false)
    assert.equal(completed, true)
  } finally {
    client.stop()
    clearTimeout(closeTimer)
  }
})

test("an unfinished 204 response is aborted at the quality request deadline", async () => {
  const errors = []
  let aborted = false
  const client = new QualityClient({
    url: () => "https://device.example/api/quality",
    onState: assert.fail,
    onError: (error) => errors.push(error),
    intervalMs: 60000,
    timeoutMs: 20,
    fetch: async (_url, options) => ({
      status: 204,
      arrayBuffer: () =>
        new Promise((_resolve, reject) => {
          options.signal.addEventListener(
            "abort",
            () => {
              aborted = true
              reject(options.signal.reason)
            },
            { once: true },
          )
        }),
    }),
  })
  client.start()
  try {
    await eventually(() => errors.length === 1)
    assert.equal(aborted, true)
  } finally {
    client.stop()
  }
})
