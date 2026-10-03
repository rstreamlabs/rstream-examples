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
      !intentionalQualityCancellation(diagnostic, browserEvents),
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
