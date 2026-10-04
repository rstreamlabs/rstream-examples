// Playwright serializes this function into a fresh, disconnected viewer page.
// Observe the actual click and compositor callback, not polling completion.
export function installStartupTiming() {
  window.__rstreamStartupTiming?.stop();
  const video = document.querySelector("#video");
  const connect = document.querySelector("#connect");
  const supported =
    typeof video?.requestVideoFrameCallback === "function" &&
    typeof connect?.addEventListener === "function";
  let stopped = false;
  let callbackID = null;
  let requestedAt = null;
  let firstFrame = null;
  const finite = (value) => (Number.isFinite(value) ? value : null);
  const record = (kind, elapsedMilliseconds, fields = {}) => {
    window.__rstreamQualificationTelemetry?.events.push({
      kind,
      elapsedMilliseconds,
      ...fields,
    });
  };
  const onRequest = () => {
    if (stopped || requestedAt !== null) return;
    requestedAt = performance.now();
    record("playback-requested", requestedAt);
    callbackID = video.requestVideoFrameCallback((now, metadata) => {
      callbackID = null;
      if (stopped || firstFrame !== null) return;
      firstFrame = {
        callbackMilliseconds: finite(now),
        expectedDisplayMilliseconds: finite(metadata.expectedDisplayTime),
        width: finite(metadata.width),
        height: finite(metadata.height),
        presentedFrames: finite(metadata.presentedFrames),
      };
      record("first-presented-frame", now, firstFrame);
    });
  };
  window.__rstreamStartupTiming = {
    snapshot() {
      const valid =
        requestedAt !== null &&
        firstFrame?.callbackMilliseconds !== null &&
        firstFrame?.callbackMilliseconds >= requestedAt &&
        firstFrame?.expectedDisplayMilliseconds !== null &&
        firstFrame?.expectedDisplayMilliseconds >= requestedAt &&
        firstFrame?.width > 0 &&
        firstFrame?.height > 0 &&
        // A later callback cannot establish when the first frame appeared.
        firstFrame?.presentedFrames === 1;
      return {
        supported,
        measurementValid: Boolean(valid),
        requestedAtMilliseconds: requestedAt,
        firstFrame,
        requestToCallbackMilliseconds: valid
          ? firstFrame.callbackMilliseconds - requestedAt
          : null,
        requestToExpectedDisplayMilliseconds: valid
          ? firstFrame.expectedDisplayMilliseconds - requestedAt
          : null,
      };
    },
    stop() {
      if (stopped) return;
      stopped = true;
      connect?.removeEventListener?.("click", onRequest, true);
      if (callbackID !== null) video.cancelVideoFrameCallback(callbackID);
      callbackID = null;
    },
  };
  if (supported) connect.addEventListener("click", onRequest, true);
  return supported;
}
