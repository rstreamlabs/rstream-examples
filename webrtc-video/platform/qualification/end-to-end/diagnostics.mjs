export function expectedBrowserDiagnostic(diagnostic, signalingResponses = []) {
  if (stoppedObservationDiagnostic(diagnostic, signalingResponses)) return true
  if (successfulNoContentAbort(diagnostic, signalingResponses)) {
    return true
  }
  if (
    new Set([
      "navigation-started",
      "mediamtx-stop-requested",
      "browser-close-requested",
    ]).has(diagnostic.phase)
  ) {
    return (
      diagnostic.type === "request-failed" &&
      diagnostic.message.endsWith(" net::ERR_ABORTED") &&
      isWHEPRequest(diagnostic.message)
    )
  }
  if (diagnostic.phase === "mediamtx-stopped") {
    return expectedStoppedMediaMTXDiagnostic(diagnostic)
  }
  if (diagnostic.phase === "platform-reload-requested") {
    return (
      diagnostic.type === "request-failed" &&
      diagnostic.message.endsWith(" net::ERR_ABORTED") &&
      (isWHEPRequest(diagnostic.message) ||
        diagnostic.message.includes("/api/devices/"))
    )
  }
  return false
}

function stoppedObservationDiagnostic(diagnostic, responses) {
  if (!Number.isFinite(diagnostic.observedAt)) return false
  const path =
    "https?:\\/\\/\\S+\\/api\\/devices\\/[^/?\\s]+\\/(metrics|recordings)"
  const pattern =
    diagnostic.type === "http-error"
      ? new RegExp(`^GET (${path}) 503$`)
      : diagnostic.type === "request-failed"
        ? new RegExp(`^GET (${path}) net::ERR_ABORTED$`)
        : diagnostic.type === "console-error"
          ? new RegExp(
              `^(${path}):[0-9]+:[0-9]+ Failed to load resource: the server responded with a status of 503 \\(Service Unavailable\\)$`,
            )
          : null
  const match = pattern?.exec(diagnostic.message)
  if (!match) return false
  const phases = ["mediamtx-stop-requested", "mediamtx-stopped"]
  // History polling continues while live viewing falls back to the producer;
  // MediaMTX remains deliberately stopped throughout these phases too.
  if (match[2] === "recordings")
    phases.push(
      "direct-fallback-playing",
      "direct-source-formats-passed",
      // Native MediaMTX rejects an index containing a partial ENOSPC segment.
      // The recording test independently requires eventual retention recovery
      // and uninterrupted live frames throughout this bounded fault interval.
      "recording-storage-full",
      "recording-storage-recovering",
    )
  if (!phases.includes(diagnostic.phase)) return false
  // The observation client discards the unavailable response body. An aborted
  // body is expected only after this same GET actually received a 503 during
  // the deliberately stopped-server phase; arbitrary timeouts still fail.
  return responses.some(
    (response) =>
      response.method === "GET" &&
      response.url === match[1] &&
      response.status === 503 &&
      Number.isFinite(response.observedAt) &&
      Math.abs(response.observedAt - diagnostic.observedAt) <= 1000,
  )
}

function successfulNoContentAbort(diagnostic, signalingResponses) {
  if (
    diagnostic.type !== "request-failed" ||
    !diagnostic.message.endsWith(" net::ERR_ABORTED") ||
    !(
      isWHEPRequest(diagnostic.message) ||
      /^GET https?:\/\/\S+\/api\/devices\/[^/?\s]+\/(quality|recordings) net::ERR_ABORTED$/.test(
        diagnostic.message,
      )
    ) ||
    !Number.isFinite(diagnostic.observedAt)
  ) {
    return false
  }
  const match = /^(GET|POST|PATCH|DELETE) (\S+) net::ERR_ABORTED$/.exec(
    diagnostic.message,
  )
  if (!match) {
    return false
  }
  const [, method, url] = match
  return signalingResponses.some(
    (response) =>
      response.method === method &&
      response.url === url &&
      response.status === 204 &&
      Number.isFinite(response.observedAt) &&
      Math.abs(response.observedAt - diagnostic.observedAt) <= 1_000,
  )
}

function expectedStoppedMediaMTXDiagnostic(diagnostic) {
  if (
    diagnostic.type === "request-failed" &&
    diagnostic.message.endsWith(" net::ERR_ABORTED") &&
    isViewerRequest(diagnostic.message)
  ) {
    return true
  }
  if (
    diagnostic.type === "request-failed" &&
    (diagnostic.message.endsWith(" net::ERR_CONNECTION_REFUSED") ||
      diagnostic.message.endsWith(" net::ERR_FAILED"))
  ) {
    return isWHEPRequest(diagnostic.message)
  }
  if (
    diagnostic.type === "console-error" &&
    diagnostic.message.endsWith(
      "Failed to load resource: net::ERR_CONNECTION_REFUSED",
    )
  ) {
    return isWHEPRequest(diagnostic.message)
  }
  if (
    diagnostic.type === "console-error" &&
    diagnostic.message.endsWith("Failed to load resource: net::ERR_FAILED")
  ) {
    return isWHEPRequest(diagnostic.message)
  }
  if (
    diagnostic.type === "console-error" &&
    diagnostic.message.includes("has been blocked by CORS policy") &&
    diagnostic.message.includes(
      "No 'Access-Control-Allow-Origin' header is present",
    )
  ) {
    return /Access to fetch at 'https?:\/\/[^']+\/[^']*whep(?:[/?][^']*)?'/.test(
      diagnostic.message,
    )
  }
  return (
    diagnostic.type === "console-warning" &&
    diagnostic.message.includes("WHEP remote session cleanup was incomplete") &&
    diagnostic.message.includes("outcome: request-error") &&
    diagnostic.message.includes("distributor: mediamtx")
  )
}

function isWHEPRequest(message) {
  return /(?:^|\s)https?:\/\/[^\s]+\/[^\s]*whep(?:[/?]|\s|$|:\d+:\d+(?:\s|$))/.test(
    message,
  )
}

function isViewerRequest(message) {
  return /(?:^|\s)POST https?:\/\/[^\s]+\/api\/devices\/[^/?\s]+\/viewer(?:[?\s]|$)/.test(
    message,
  )
}
