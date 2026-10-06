import { expectedBrowserDiagnostic } from "./diagnostics.mjs"

export async function drainBrowserEvents(page, destination) {
  if (!page || page.isClosed()) {
    return
  }
  const observed = await page
    .evaluate(() => {
      const current = Array.isArray(window.__rstreamQualificationEvents)
        ? window.__rstreamQualificationEvents
        : []
      window.__rstreamQualificationEvents = []
      return current
    })
    .catch(() => [])
  destination.push(...observed)
}

export function unexpectedBrowserDiagnostics(
  diagnostics,
  signalingResponses = [],
  browserEvents = [],
) {
  return diagnostics.filter(
    (diagnostic) =>
      !expectedBrowserDiagnostic(diagnostic, signalingResponses) &&
      !intentionalQualityCancellation(diagnostic, browserEvents) &&
      !completedRecordingIndexRead(diagnostic, browserEvents) &&
      !intentionalRecordingCancellation(diagnostic, browserEvents),
  )
}

function completedRecordingIndexRead(diagnostic, events) {
  if (diagnostic.type !== "request-failed" || !Number.isFinite(diagnostic.at))
    return false
  const match =
    /^GET (https?:\/\/\S+\/api\/devices\/[^/?\s]+\/recordings) net::ERR_ABORTED$/.exec(
      diagnostic.message,
    )
  if (!match) return false
  // As for quality JSON, require the complete parsed body in the browser.
  // A 200 header or an arbitrary abort never establishes successful delivery.
  return events.some(
    (event) =>
      event.name === "recording-index-response-read" &&
      event.method === "GET" &&
      event.url === match[1] &&
      event.status === 200 &&
      Number.isFinite(event.at) &&
      Math.abs(event.at - diagnostic.at) <= 1000,
  )
}

function intentionalRecordingCancellation(diagnostic, events) {
  if (
    diagnostic.type !== "request-failed" ||
    diagnostic.phase !== "recording-return-live-requested" ||
    !Number.isFinite(diagnostic.at)
  )
    return false
  const match =
    /^GET (https?:\/\/\S+\/api\/devices\/[^/?\s]+\/recordings\/playback\?\S+) net::ERR_ABORTED$/.exec(
      diagnostic.message,
    )
  if (!match) return false
  return events.some(
    (event) =>
      event.name === "recording-return-live-requested" &&
      event.url === match[1] &&
      Number.isFinite(event.at) &&
      Math.abs(event.at - diagnostic.at) <= 1000,
  )
}

function intentionalQualityCancellation(diagnostic, events) {
  if (diagnostic.type !== "request-failed" || !Number.isFinite(diagnostic.at))
    return false
  const match =
    /^GET (https?:\/\/\S+\/api\/devices\/[^/?\s]+\/quality) net::ERR_ABORTED$/.exec(
      diagnostic.message,
    )
  if (!match) return false
  // Chromium can report ERR_ABORTED after a streamed JSON body was consumed.
  // A 200 header alone is insufficient: require the browser's complete-body
  // observation for this URL/method at the same time. Truncation/timeouts fail.
  if (
    events.some(
      (event) =>
        event.name === "quality-response-read" &&
        event.method === "GET" &&
        event.url === match[1] &&
        event.status === 200 &&
        Number.isFinite(event.at) &&
        Math.abs(event.at - diagnostic.at) <= 1000,
    )
  )
    return true
  return events.some(
    (event) =>
      event.name === "quality-request-aborted" &&
      event.method === "GET" &&
      event.url === match[1] &&
      [
        "Error: Source quality request superseded",
        "Error: Source quality client stopped",
      ].includes(event.reason) &&
      Number.isFinite(event.at) &&
      Math.abs(event.at - diagnostic.at) <= 1000,
  )
}
