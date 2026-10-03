import assert from "node:assert/strict"
import test from "node:test"

import { expectedBrowserDiagnostic } from "../qualification/end-to-end/diagnostics.mjs"
import {
  drainBrowserEvents,
  unexpectedBrowserDiagnostics,
} from "../qualification/end-to-end/evidence.mjs"

test("quality aborts require a matching intentional cancellation, never a timeout", () => {
  const url = "http://localhost:3000/api/devices/camera/quality"
  const diagnostic = {
    at: 1000,
    type: "request-failed",
    message: `GET ${url} net::ERR_ABORTED`,
    phase: "quality-started",
  }
  const event = {
    name: "quality-request-aborted",
    method: "GET",
    url,
    at: 990,
    reason: "Error: Source quality request superseded",
  }
  assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], [], [event]), [])
  for (const events of [
    [],
    [{ ...event, reason: "Error: Source quality request timed out." }],
    [{ ...event, url: `${url}-other` }],
    [{ ...event, at: 3000 }],
  ]) {
    assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], [], events), [
      diagnostic,
    ])
  }
})

test("completed quality responses require a browser JSON-body observation, not just 200 headers", () => {
  const url = "http://localhost:3000/api/devices/camera/quality"
  const diagnostic = {
    at: 1000,
    type: "request-failed",
    message: `GET ${url} net::ERR_ABORTED`,
  }
  const body = {
    name: "quality-response-read",
    method: "GET",
    url,
    at: 990,
    status: 200,
  }
  assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], [], [body]), [])
  assert.deepEqual(
    unexpectedBrowserDiagnostics([diagnostic], [{ ...body, observedAt: 990 }]),
    [diagnostic],
  )
  for (const event of [
    { ...body, status: 503 },
    { ...body, at: 3000 },
    { ...body, url: `${url}-other` },
  ])
    assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], [], [event]), [
      diagnostic,
    ])
})

test("metrics outage diagnostics require the matching 503 and deliberate stopped phase", () => {
  const url = "http://localhost:3000/api/devices/camera/metrics"
  const response = { method: "GET", url, status: 503, observedAt: 1000 }
  for (const [type, message] of [
    ["http-error", `GET ${url} 503`],
    ["request-failed", `GET ${url} net::ERR_ABORTED`],
    [
      "console-error",
      `${url}:0:0 Failed to load resource: the server responded with a status of 503 (Service Unavailable)`,
    ],
  ]) {
    const diagnostic = {
      type,
      message,
      phase: "mediamtx-stopped",
      observedAt: 1001,
    }
    assert.equal(expectedBrowserDiagnostic(diagnostic, [response]), true)
    for (const records of [
      [],
      [{ ...response, status: 200 }],
      [{ ...response, url: `${url}-other` }],
      [{ ...response, method: "PUT" }],
      [{ ...response, observedAt: 3000 }],
    ])
      assert.equal(expectedBrowserDiagnostic(diagnostic, records), false)
    for (const change of [
      { phase: "mediamtx-playing" },
      { phase: "mediamtx-recovered" },
      { observedAt: NaN },
      { message: message.replace("metrics", "quality") },
      { message: message.replace("503", "500") },
      { message: message.replace("ERR_ABORTED", "ERR_FAILED") },
    ]) {
      if (change.message === message) continue
      assert.equal(
        expectedBrowserDiagnostic({ ...diagnostic, ...change }, [response]),
        false,
      )
    }
  }
})

test("platform qualification accepts only diagnostics caused by deliberate transitions", () => {
  const accepted = [
    {
      message:
        "PATCH http://localhost:8889/devices/device-id/whep/session-id net::ERR_ABORTED",
      phase: "navigation-started",
      type: "request-failed",
    },
    {
      message:
        "POST http://localhost:8889/devices/device-id/whep net::ERR_ABORTED",
      phase: "mediamtx-stop-requested",
      type: "request-failed",
    },
    {
      message:
        "DELETE http://localhost:8889/devices/device-id/whep/session-id net::ERR_ABORTED",
      phase: "browser-close-requested",
      type: "request-failed",
    },
    {
      message:
        "PATCH https://media.example/devices/device-id/whep/session-id?rstream.token=[redacted] net::ERR_ABORTED",
      phase: "mediamtx-stop-requested",
      type: "request-failed",
    },
    {
      message:
        "POST http://localhost:3000/api/devices/device-id/viewer net::ERR_ABORTED",
      phase: "platform-reload-requested",
      type: "request-failed",
    },
    {
      message:
        "PATCH http://localhost:8889/devices/device-id/whep/session-id net::ERR_CONNECTION_REFUSED",
      phase: "mediamtx-stopped",
      type: "request-failed",
    },
    {
      message:
        "POST http://localhost:3000/api/devices/device-id/viewer net::ERR_ABORTED",
      phase: "mediamtx-stopped",
      type: "request-failed",
    },
    {
      message:
        "http://localhost:8889/devices/device-id/whep/session-id:0:0 Failed to load resource: net::ERR_CONNECTION_REFUSED",
      phase: "mediamtx-stopped",
      type: "console-error",
    },
    {
      message:
        "PATCH https://media.example/devices/device-id/whep/session-id net::ERR_FAILED",
      phase: "mediamtx-stopped",
      type: "request-failed",
    },
    {
      message:
        "https://media.example/devices/device-id/whep/session-id:0:0 Failed to load resource: net::ERR_FAILED",
      phase: "mediamtx-stopped",
      type: "console-error",
    },
    {
      message:
        "http://localhost:3000/:0:0 Access to fetch at 'https://media.example/devices/device-id/whep/session-id' from origin 'http://localhost:3000' has been blocked by CORS policy: Response to preflight request doesn't pass access control check: No 'Access-Control-Allow-Origin' header is present on the requested resource.",
      phase: "mediamtx-stopped",
      type: "console-error",
    },
    {
      message:
        "http://localhost:3000/chunk.js:1:1 WHEP remote session cleanup was incomplete {outcome: request-error, distributor: mediamtx}",
      phase: "mediamtx-stopped",
      type: "console-warning",
    },
  ]
  for (const diagnostic of accepted) {
    assert.equal(expectedBrowserDiagnostic(diagnostic), true)
  }
  const rejected = [
    {
      ...accepted[0],
      message: accepted[0].message.replace("ERR_ABORTED", "ERR_FAILED"),
    },
    {
      ...accepted[0],
      message: "GET http://localhost:8889/health net::ERR_ABORTED",
    },
    { ...accepted[0], phase: "mediamtx-playing" },
    { ...accepted[0], type: "console-error" },
    {
      ...accepted.at(-1),
      message: accepted
        .at(-1)
        .message.replace("distributor: mediamtx", "distributor: direct"),
    },
    {
      ...accepted.at(-2),
      message: accepted.at(-2).message.replace("/whep/", "/health/"),
    },
  ]
  for (const diagnostic of rejected) {
    assert.equal(expectedBrowserDiagnostic(diagnostic), false)
  }
})

test("platform qualification drains browser events without duplicating later evidence", async () => {
  const batches = [
    [{ name: "rstream:video-distributor-fallback", observedAt: 10 }],
    [{ name: "rstream:whep-close", observedAt: 20 }],
  ]
  const page = {
    evaluate: async () => batches.shift() ?? [],
    isClosed: () => false,
  }
  const events = []
  await drainBrowserEvents(page, events)
  await drainBrowserEvents(page, events)
  assert.deepEqual(events, [
    { name: "rstream:video-distributor-fallback", observedAt: 10 },
    { name: "rstream:whep-close", observedAt: 20 },
  ])
})

test("platform qualification preserves the diagnostic that caused an early failure", () => {
  const expected = {
    message:
      "POST http://localhost:8889/devices/device-id/whep net::ERR_CONNECTION_REFUSED",
    phase: "mediamtx-stopped",
    type: "request-failed",
  }
  const unexpected = {
    message: "GET http://localhost:3000/api/devices/device-id/viewer 500",
    phase: "navigation-started",
    type: "http-error",
  }
  assert.deepEqual(unexpectedBrowserDiagnostics([expected, unexpected]), [
    unexpected,
  ])
})

test("platform qualification accepts a WHEP abort only after the same request returned 204", () => {
  const completed = {
    message:
      "PATCH https://media.example/whep/session?rstream.token=[redacted] net::ERR_ABORTED",
    observedAt: 2_010,
    phase: "mediamtx-stopped",
    type: "request-failed",
  }
  const response = {
    method: "PATCH",
    observedAt: 2_000,
    status: 204,
    url: "https://media.example/whep/session?rstream.token=[redacted]",
  }
  assert.deepEqual(unexpectedBrowserDiagnostics([completed], [response]), [])
  for (const mismatched of [
    [],
    [{ ...response, method: "DELETE" }],
    [{ ...response, status: 500 }],
    [{ ...response, url: "https://media.example/whep/another" }],
    [{ ...response, observedAt: 500 }],
  ]) {
    assert.deepEqual(unexpectedBrowserDiagnostics([completed], mismatched), [
      completed,
    ])
  }
})

test("quality without presets accepts only a correlated, completed 204 response", () => {
  const url = "https://platform.example/api/devices/device-id/quality"
  const diagnostic = {
    type: "request-failed",
    message: `GET ${url} net::ERR_ABORTED`,
    observedAt: 2010,
    phase: "mediamtx-playing",
  }
  const response = { method: "GET", url, status: 204, observedAt: 2000 }
  assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], [response]), [])
  for (const responses of [
    [],
    [{ ...response, status: 200 }],
    [{ ...response, method: "PUT" }],
    [{ ...response, observedAt: 0 }],
    [{ ...response, url: `${url}/another` }],
  ]) {
    assert.deepEqual(unexpectedBrowserDiagnostics([diagnostic], responses), [
      diagnostic,
    ])
  }
})

test("platform qualification tolerates an unavailable page while writing failure evidence", async () => {
  const events = []
  await drainBrowserEvents(undefined, events)
  await drainBrowserEvents(
    {
      evaluate: async () => {
        throw new Error("target closed")
      },
      isClosed: () => false,
    },
    events,
  )
  assert.deepEqual(events, [])
})

test("required MediaMTX retries identify root WHEP console locations only during an intentional outage", () => {
  const diagnostic = {
    message:
      "http://localhost:8889/devices/device-id/whep:0:0 Failed to load resource: net::ERR_CONNECTION_REFUSED",
    phase: "mediamtx-stopped",
    type: "console-error",
  }
  assert.equal(expectedBrowserDiagnostic(diagnostic), true)
  assert.equal(
    expectedBrowserDiagnostic({ ...diagnostic, phase: "mediamtx-playing" }),
    false,
  )
  assert.equal(
    expectedBrowserDiagnostic({
      ...diagnostic,
      message: diagnostic.message.replace("whep:0:0", "health:0:0"),
    }),
    false,
  )
})
