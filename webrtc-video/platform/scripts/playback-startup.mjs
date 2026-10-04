// Serialized by Playwright before navigation. Qualification only: retain timings,
// never URLs, response bodies, session cookies or viewer credentials.
export function installPlaybackStartupTiming() {
  window.__playbackStartup?.stop()
  const nativeFetch = window.fetch
  const NativePeer = window.RTCPeerConnection
  let stopped = false
  let video = null
  let callbackID = null
  let firstFrame = null
  let firstVisibleFrame = null
  let activationRequestedAtMilliseconds = null
  let activationMarks = 0
  let supported = null
  let armedAtMilliseconds = null
  let readyStateAtArm = null
  const longTasks = []
  const mediaEvents = []
  const videoListeners = []
  const taskObserver =
    typeof PerformanceObserver === "function" &&
    PerformanceObserver.supportedEntryTypes?.includes("longtask")
      ? new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            if (longTasks.length < 64)
              longTasks.push({
                startedAt: entry.startTime,
                duration: entry.duration,
              })
          }
        })
      : null
  taskObserver?.observe({ type: "longtask", buffered: true })
  const authorization = []
  const whep = []
  const peers = []
  const peerListeners = []
  window.__discoveryPeers = []
  const finite = (value) => (Number.isFinite(value) ? value : null)
  const installedAtMilliseconds = performance.now()
  performance.mark?.("rstream-startup-probe-installed")
  const classify = (input, init) => {
    try {
      const url = new URL(
        typeof input === "string" || input instanceof URL ? input : input.url,
        location.href,
      )
      const method = (init?.method ?? input?.method ?? "GET").toUpperCase()
      if (method !== "POST") return null
      if (
        url.origin === location.origin &&
        /^\/api\/devices\/[^/]+\/viewer$/.test(url.pathname)
      )
        return authorization
      if (url.pathname.endsWith("/whep")) return whep
    } catch {
      // The real fetch retains responsibility for invalid request arguments.
    }
    return null
  }
  const observedFetch = async function (input, init) {
    const observations = stopped ? null : classify(input, init)
    // Bound telemetry and reject retries as a first-attempt measurement below.
    const event =
      observations && observations.length < 8
        ? { startedAt: performance.now(), headersAt: null, status: null }
        : null
    if (event) observations.push(event)
    const response = await Reflect.apply(nativeFetch, this, [input, init])
    if (event && !stopped) {
      event.headersAt = performance.now()
      event.status = response.status
      if (observations === authorization) {
        const originalJSON = response.json
        response.json = async function (...args) {
          const body = await Reflect.apply(originalJSON, this, args)
          if (!stopped) event.bodyReadAt = performance.now()
          return body
        }
      }
    }
    return response
  }
  class ObservedPeer extends NativePeer {
    constructor(...args) {
      super(...args)
      if (stopped) return
      window.__discoveryPeers.push(this)
      const event = { createdAt: performance.now(), connectedAt: null }
      peers.push(event)
      const onState = () => {
        if (
          !stopped &&
          event.connectedAt === null &&
          this.connectionState === "connected"
        )
          event.connectedAt = performance.now()
      }
      this.addEventListener("connectionstatechange", onState)
      peerListeners.push(() =>
        this.removeEventListener("connectionstatechange", onState),
      )
    }
  }
  const visibility = () => {
    const rect = video.getBoundingClientRect()
    const center = {
      x: rect.left + rect.width / 2,
      y: rect.top + rect.height / 2,
    }
    let elementVisible = video.isConnected && rect.width > 0 && rect.height > 0
    for (
      let element = video;
      element && elementVisible;
      element = element.parentElement
    ) {
      const style = window.getComputedStyle(element)
      if (
        style.display === "none" ||
        style.visibility !== "visible" ||
        Number(style.opacity) === 0
      )
        elementVisible = false
    }
    const centerInViewport =
      center.x >= 0 &&
      center.y >= 0 &&
      center.x < window.innerWidth &&
      center.y < window.innerHeight
    const state = {
      documentVisible: document.visibilityState === "visible",
      elementVisible,
      centerInViewport,
      centerUnobstructed:
        centerInViewport &&
        document.elementFromPoint(center.x, center.y) === video,
      coveredByStatus: Boolean(
        video.parentElement?.querySelector(":scope > .absolute"),
      ),
      mediaReady:
        video.readyState >= 2 &&
        !video.paused &&
        video.videoWidth > 0 &&
        video.videoHeight > 0,
    }
    return {
      ...state,
      visible:
        state.documentVisible &&
        state.elementVisible &&
        state.centerInViewport &&
        state.centerUnobstructed &&
        !state.coveredByStatus &&
        state.mediaReady,
    }
  }
  const validFrame = (frame) =>
    Boolean(
      Number.isFinite(frame?.callbackMilliseconds) &&
      Number.isFinite(frame?.observedAtMilliseconds) &&
      Number.isFinite(frame?.expectedDisplayMilliseconds) &&
      frame.width > 0 &&
      frame.height > 0 &&
      Number.isInteger(frame.presentedFrames) &&
      frame.presentedFrames > 0,
    )
  const onFrame = (now, metadata) => {
    callbackID = null
    if (stopped || firstVisibleFrame) return
    const frame = {
      callbackMilliseconds: finite(now),
      presentationMilliseconds: finite(metadata.presentationTime),
      expectedDisplayMilliseconds: finite(metadata.expectedDisplayTime),
      receiveMilliseconds: finite(metadata.receiveTime),
      mediaTimeSeconds: finite(metadata.mediaTime),
      width: finite(metadata.width),
      height: finite(metadata.height),
      presentedFrames: finite(metadata.presentedFrames),
      visibility: visibility(),
      // Read after visibility/layout checks. A delayed callback or a loading
      // overlay must never produce a timestamp earlier than this observation.
      observedAtMilliseconds: performance.now(),
    }
    if (!firstFrame) {
      firstFrame = frame
      performance.mark?.("rstream-startup-first-callback")
    }
    if (frame.visibility.visible && validFrame(frame)) {
      firstVisibleFrame = frame
      performance.mark?.("rstream-startup-first-visible-callback")
    } else callbackID = video.requestVideoFrameCallback(onFrame)
  }
  const observeVideo = () => {
    if (stopped || video) return
    video = document.querySelector(".video-player-picture > video")
    if (!video) return
    observer.disconnect()
    supported = typeof video.requestVideoFrameCallback === "function"
    if (!supported) return
    armedAtMilliseconds = performance.now()
    readyStateAtArm = video.readyState
    for (const kind of ["loadeddata", "playing"]) {
      const listener = () => {
        if (stopped || mediaEvents.length >= 8) return
        mediaEvents.push({
          kind,
          atMilliseconds: performance.now(),
          coveredByStatus: Boolean(
            video.parentElement?.querySelector(":scope > .absolute"),
          ),
        })
      }
      video.addEventListener?.(kind, listener)
      videoListeners.push(() => video.removeEventListener?.(kind, listener))
    }
    callbackID = video.requestVideoFrameCallback(onFrame)
  }
  const observer = new MutationObserver(observeVideo)
  const stop = () => {
    if (stopped) return
    stopped = true
    observer.disconnect()
    taskObserver?.disconnect()
    if (callbackID !== null) video.cancelVideoFrameCallback(callbackID)
    callbackID = null
    for (const remove of peerListeners) remove()
    for (const remove of videoListeners) remove()
    window.removeEventListener("pagehide", stop)
    if (window.fetch === observedFetch) window.fetch = nativeFetch
    if (window.RTCPeerConnection === ObservedPeer)
      window.RTCPeerConnection = NativePeer
  }
  window.__playbackStartup = {
    stop,
    markActivation() {
      if (stopped) return
      activationMarks++
      activationRequestedAtMilliseconds = performance.now()
    },
    snapshot() {
      const auth = authorization[0]
      const post = whep[0]
      const display = firstFrame?.expectedDisplayMilliseconds
      const sequenceValid = Boolean(
        supported &&
        authorization.length === 1 &&
        whep.length === 1 &&
        peers.length === 1 &&
        peers[0].connectedAt !== null &&
        auth?.status === 200 &&
        post?.status === 201 &&
        Number.isFinite(auth.startedAt) &&
        auth.startedAt >= 0 &&
        auth.headersAt >= auth.startedAt &&
        Number.isFinite(auth.bodyReadAt) &&
        auth.bodyReadAt >= auth.headersAt &&
        post.startedAt >= auth.bodyReadAt &&
        post.headersAt >= post.startedAt &&
        armedAtMilliseconds !== null &&
        armedAtMilliseconds <= auth.startedAt &&
        readyStateAtArm === 0 &&
        activationMarks <= 1 &&
        (activationRequestedAtMilliseconds === null ||
          activationRequestedAtMilliseconds <= auth.startedAt),
      )
      const valid = Boolean(
        sequenceValid &&
        Number.isFinite(display) &&
        display >= post.headersAt &&
        Number.isFinite(firstFrame?.callbackMilliseconds) &&
        firstFrame?.callbackMilliseconds >= post.headersAt &&
        firstFrame?.width > 0 &&
        firstFrame?.height > 0 &&
        firstFrame?.presentedFrames === 1,
      )
      const visible = firstVisibleFrame
      const visiblePresentationValid = Boolean(
        sequenceValid &&
        validFrame(visible) &&
        visible.visibility.visible &&
        visible.callbackMilliseconds >= post.headersAt &&
        visible.expectedDisplayMilliseconds >= post.headersAt &&
        visible.observedAtMilliseconds >= post.headersAt,
      )
      const visibleAt = visiblePresentationValid
        ? Math.max(
            visible.expectedDisplayMilliseconds,
            visible.observedAtMilliseconds,
          )
        : null
      return {
        schemaVersion: 2,
        supported,
        installedAtMilliseconds,
        activationRequestedAtMilliseconds,
        navigationTimeOrigin: performance.timeOrigin,
        armedAtMilliseconds,
        readyStateAtArm,
        longTasks,
        mediaEvents,
        measurementValid: valid,
        exactFirstSubmissionMeasured: valid,
        authorizationResponseMilliseconds: sequenceValid
          ? auth.bodyReadAt - auth.startedAt
          : null,
        authorization,
        whep,
        peers,
        firstFrame,
        firstVisibleFrame,
        visiblePresentationValid,
        visiblePresentationScope:
          "First observed unoccluded video frame callback; later of browser expected-display time and observation after visibility checks. Conservative startup estimate, not exact first submission or physical display latency.",
        navigationToVisiblePresentationMilliseconds: visibleAt,
        authorizationToVisiblePresentationMilliseconds:
          visibleAt === null ? null : visibleAt - auth.startedAt,
        activationToVisiblePresentationMilliseconds:
          visibleAt === null || activationRequestedAtMilliseconds === null
            ? null
            : visibleAt - activationRequestedAtMilliseconds,
        navigationToExpectedDisplayMilliseconds: valid ? display : null,
        authorizationToExpectedDisplayMilliseconds: valid
          ? display - auth.startedAt
          : null,
        authorizationMilliseconds: valid
          ? auth.bodyReadAt - auth.startedAt
          : null,
      }
    },
  }
  window.fetch = observedFetch
  window.RTCPeerConnection = ObservedPeer
  window.addEventListener("pagehide", stop, { once: true })
  observer.observe(document, { childList: true, subtree: true })
  observeVideo()
}
