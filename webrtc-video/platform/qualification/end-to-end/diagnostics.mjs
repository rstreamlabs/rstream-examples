export function expectedBrowserDiagnostic(diagnostic) {
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
