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
  let firstVisibleFrame = null;
  const finite = (value) => (Number.isFinite(value) ? value : null);
  const record = (kind, elapsedMilliseconds, fields = {}) => {
    window.__rstreamQualificationTelemetry?.events.push({
      kind,
      elapsedMilliseconds,
      ...fields,
    });
  };
  const visibility = () => {
    const rect = video.getBoundingClientRect();
    const center = {
      x: rect.left + rect.width / 2,
      y: rect.top + rect.height / 2,
    };
    let elementVisible = video.isConnected && rect.width > 0 && rect.height > 0;
    for (
      let element = video;
      element && elementVisible;
      element = element.parentElement
    ) {
      const style = window.getComputedStyle(element);
      if (
        style.display === "none" ||
        style.visibility !== "visible" ||
        Number(style.opacity) === 0
      )
        elementVisible = false;
    }
    const centerInViewport =
      center.x >= 0 &&
      center.y >= 0 &&
      center.x < window.innerWidth &&
      center.y < window.innerHeight;
    const state = {
      documentVisible: document.visibilityState === "visible",
      elementVisible,
      centerInViewport,
      centerUnobstructed:
        centerInViewport &&
        document.elementFromPoint(center.x, center.y) === video,
      mediaReady:
        video.readyState >= 2 &&
        !video.paused &&
        !video.ended &&
        video.videoWidth > 0 &&
        video.videoHeight > 0,
    };
    return {
      ...state,
      visible: Object.values(state).every((value) => value === true),
    };
  };
  const validTiming = (frame) =>
    requestedAt !== null &&
    Number.isFinite(frame?.callbackMilliseconds) &&
    frame.callbackMilliseconds >= requestedAt &&
    Number.isFinite(frame?.observedAtMilliseconds) &&
    frame.observedAtMilliseconds >= requestedAt &&
    Number.isFinite(frame?.expectedDisplayMilliseconds) &&
    frame.expectedDisplayMilliseconds >= requestedAt &&
    frame.width > 0 &&
    frame.height > 0 &&
    Number.isSafeInteger(frame.presentedFrames) &&
    frame.presentedFrames > 0;
  const onFrame = (now, metadata) => {
    callbackID = null;
    if (stopped || firstVisibleFrame !== null) return;
    const frame = {
      callbackMilliseconds: finite(now),
      expectedDisplayMilliseconds: finite(metadata.expectedDisplayTime),
      width: finite(metadata.width),
      height: finite(metadata.height),
      presentedFrames: finite(metadata.presentedFrames),
      visibility: visibility(),
      observedAtMilliseconds: finite(performance.now()),
    };
    if (firstFrame === null) {
      firstFrame = frame;
      record("first-presented-frame", now, firstFrame);
    }
    if (validTiming(frame) && frame.visibility.visible) {
      firstVisibleFrame = frame;
      record("first-visible-frame", frame.observedAtMilliseconds, frame);
      return;
    }
    callbackID = video.requestVideoFrameCallback(onFrame);
  };
  const onRequest = () => {
    if (stopped || requestedAt !== null) return;
    requestedAt = performance.now();
    record("playback-requested", requestedAt);
    callbackID = video.requestVideoFrameCallback(onFrame);
  };
  window.__rstreamStartupTiming = {
    snapshot() {
      const valid =
        validTiming(firstFrame) &&
        // A later callback cannot establish the exact first submission.
        firstFrame?.presentedFrames === 1;
      const visibleValid =
        validTiming(firstVisibleFrame) && firstVisibleFrame.visibility.visible;
      return {
        supported,
        measurementValid: Boolean(valid),
        requestedAtMilliseconds: requestedAt,
        firstFrame,
        firstVisibleFrame,
        visiblePresentationValid: Boolean(visibleValid),
        visiblePresentationScope:
          "First observed unobstructed browser frame; conservative duration, not exact first submission or physical display timing",
        requestToVisiblePresentationMilliseconds: visibleValid
          ? Math.max(
              firstVisibleFrame.observedAtMilliseconds,
              firstVisibleFrame.expectedDisplayMilliseconds,
            ) - requestedAt
          : null,
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
