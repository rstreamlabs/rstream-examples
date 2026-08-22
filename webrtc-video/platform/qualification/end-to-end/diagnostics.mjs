export function expectedBrowserDiagnostic(diagnostic, signalingResponses = []) {
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

function successfulNoContentAbort(diagnostic, signalingResponses) {
  if (
    diagnostic.type !== "request-failed" ||
    !diagnostic.message.endsWith(" net::ERR_ABORTED") ||
    !isWHEPRequest(diagnostic.message) ||
    !Number.isFinite(diagnostic.observedAt)
  ) {
    return false
  }
  const match = /^(POST|PATCH|DELETE) (\S+) net::ERR_ABORTED$/.exec(
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
  return /(?:^|\s)https?:\/\/[^\s]+\/[^\s]*whep(?:[/?]|\s|$)/.test(message)
}

function isViewerRequest(message) {
  return /(?:^|\s)POST https?:\/\/[^\s]+\/api\/devices\/[^/?\s]+\/viewer(?:[?\s]|$)/.test(
    message,
  )
}
