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
    callbackID = video.requestVideoFrameCallback((now, metadata) => {
      callbackID = null
      if (stopped || firstFrame) return
      firstFrame = {
        callbackMilliseconds: finite(now),
        observedAtMilliseconds: performance.now(),
        expectedDisplayMilliseconds: finite(metadata.expectedDisplayTime),
        width: finite(metadata.width),
        height: finite(metadata.height),
        presentedFrames: finite(metadata.presentedFrames),
      }
    })
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
    snapshot() {
      const auth = authorization[0]
      const post = whep[0]
      const display = firstFrame?.expectedDisplayMilliseconds
      const valid = Boolean(
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
        Number.isFinite(display) &&
        display >= post.headersAt &&
        Number.isFinite(firstFrame?.callbackMilliseconds) &&
        firstFrame?.callbackMilliseconds >= post.headersAt &&
        firstFrame?.width > 0 &&
        firstFrame?.height > 0 &&
        firstFrame?.presentedFrames === 1,
      )
      return {
        supported,
        navigationTimeOrigin: performance.timeOrigin,
        armedAtMilliseconds,
        readyStateAtArm,
        longTasks,
        mediaEvents,
        measurementValid: valid,
        authorization,
        whep,
        peers,
        firstFrame,
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
