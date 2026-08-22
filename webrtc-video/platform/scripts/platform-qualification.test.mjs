import assert from "node:assert/strict"
import test from "node:test"

import { expectedBrowserDiagnostic } from "../qualification/end-to-end/diagnostics.mjs"
import {
  drainBrowserEvents,
  unexpectedBrowserDiagnostics,
} from "../qualification/end-to-end/evidence.mjs"

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
