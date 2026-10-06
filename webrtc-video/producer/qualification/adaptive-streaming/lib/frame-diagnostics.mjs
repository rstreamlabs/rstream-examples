// Self-contained because Playwright serializes this function into the page.
export function installFrameDiagnostics() {
  window.__rstreamFrameDiagnostics?.stop();
  const video = document.querySelector("#video");
  if (typeof video?.requestVideoFrameCallback !== "function") {
    window.__rstreamFrameDiagnostics = null;
    return false;
  }

  const eventLimit = 128;
  let events = [];
  let omittedEvents = 0;
  let frames = 0;
  let maximumFrameGapMilliseconds = 0;
  let maximumTimerDelayMilliseconds = 0;
  let previous = null;
  let stopped = false;
  let callbackID;
  let lastTimerAt = performance.now();
  const finite = (value) => (Number.isFinite(value) ? value : null);
  const record = (event) => {
    if (events.length < eventLimit) events.push(event);
    else omittedEvents += 1;
  };
  const onFrame = (now, metadata) => {
    if (stopped) return;
    const current = {
      callbackMilliseconds: now,
      presentationMilliseconds: finite(metadata.presentationTime),
      expectedDisplayMilliseconds: finite(metadata.expectedDisplayTime),
      receiveMilliseconds: finite(metadata.receiveTime),
      processingSeconds: finite(metadata.processingDuration),
      mediaSeconds: finite(metadata.mediaTime),
      rtpTimestamp: finite(metadata.rtpTimestamp),
      presentedFrames: finite(metadata.presentedFrames),
    };
    frames += 1;
    if (previous !== null) {
      const gap = now - previous.callbackMilliseconds;
      maximumFrameGapMilliseconds = Math.max(maximumFrameGapMilliseconds, gap);
      if (gap > 100) {
        record({ kind: "frame-gap", gapMilliseconds: gap, previous, current });
      }
    }
    previous = current;
    callbackID = video.requestVideoFrameCallback(onFrame);
  };
  callbackID = video.requestVideoFrameCallback(onFrame);
  const timerID = setInterval(() => {
    if (stopped) return;
    const now = performance.now();
    const delay = Math.max(0, now - lastTimerAt - 100);
    maximumTimerDelayMilliseconds = Math.max(
      maximumTimerDelayMilliseconds,
      delay,
    );
    if (delay > 50) {
      record({
        kind: "timer-delay",
        atMilliseconds: now,
        delayMilliseconds: delay,
      });
    }
    lastTimerAt = now;
  }, 100);

  window.__rstreamFrameDiagnostics = {
    drain() {
      const sample = {
        // Use this origin to correlate browser callbacks and host sample times.
        // Neither receiveTime nor mediaTime establishes source capture time.
        timeOriginMilliseconds: performance.timeOrigin,
        sampledAtMilliseconds: performance.now(),
        frames,
        maximumFrameGapMilliseconds,
        maximumTimerDelayMilliseconds,
        latest: previous,
        events,
        omittedEvents,
      };
      events = [];
      omittedEvents = 0;
      frames = 0;
      maximumFrameGapMilliseconds = 0;
      maximumTimerDelayMilliseconds = 0;
      return sample;
    },
    stop() {
      if (stopped) return;
      stopped = true;
      clearInterval(timerID);
      video.cancelVideoFrameCallback(callbackID);
    },
  };
  return true;
}
