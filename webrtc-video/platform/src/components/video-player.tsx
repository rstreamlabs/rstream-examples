"use client"

import { DistributionMetrics } from "@/components/distribution-metrics"
import { QualitySelector } from "@/components/quality-selector"
import { type RefObject } from "react"
import { useEffect } from "react"
import { useLayoutEffect } from "react"
import { useRef } from "react"
import { useState } from "react"
import { Maximize2, Minimize2 } from "lucide-react"

import { apiErrorSchema } from "@/lib/validations/device"
import { Button } from "@/components/ui/button"
import { type ViewerPayload } from "@/lib/validations/device"
import { viewerPayloadSchema } from "@/lib/validations/device"
import { PlaybackHealthTracker } from "@/lib/video-playback-health"
import { minimumUsableFramesPerSecond } from "@/lib/video-playback-health"
import {
  type ViewerClientCallbacks,
  type ViewerPhase,
  ViewerSessionController,
  viewerRequestSignal,
} from "@/lib/viewer-session"
import { WHEPClient, type WHEPCloseResult } from "@/lib/whep-client"
import { containExpandedPlayer } from "@/lib/player-expansion"

type VideoDistributor = {
  allowLegacyWildcardETag: boolean
}

// sessionRef guards against React Strict Mode remounts and stale callbacks.
export function VideoPlayer({
  deviceId,
  deviceName,
}: {
  deviceId: string
  deviceName: string
}) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const playerRef = useRef<HTMLDivElement>(null)
  const expandRef = useRef<HTMLButtonElement>(null)
  const scrollBeforeExpansion = useRef({ x: 0, y: 0 })
  const [expanded, setExpanded] = useState(false)
  const sessionRef = useRef(0)
  const [phase, setPhase] = useState<ViewerPhase>("connecting")
  const [distributor, setDistributor] = useState<
    ViewerPayload["distributor"]["kind"] | null
  >(null)
  const [mediaMTXFallback, setMediaMTXFallback] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [retryKey, setRetryKey] = useState(0)
  useLayoutEffect(() => {
    if (!expanded || !playerRef.current || !expandRef.current) return
    return containExpandedPlayer(
      playerRef.current,
      expandRef.current,
      () => setExpanded(false),
      scrollBeforeExpansion.current,
    )
  }, [expanded])
  useEffect(() => {
    const session = sessionRef.current + 1
    sessionRef.current = session
    const isCurrent = () => sessionRef.current === session
    let allowDirectFallback = true
    let playbackMonitor: ReturnType<typeof monitorPlayback> | null = null
    const setCurrentPhase = (nextPhase: ViewerPhase) => {
      if (isCurrent()) {
        setPhase(nextPhase)
      }
    }
    const fail = (err: unknown) => {
      if (!isCurrent()) {
        return
      }
      setPhase("error")
      setError(errorMessage(err))
    }
    setError(null)
    setPhase("connecting")
    setDistributor(null)
    setMediaMTXFallback(false)
    let controller: ViewerSessionController<ViewerPayload, RTCTrackEvent>
    controller = new ViewerSessionController<ViewerPayload, RTCTrackEvent>({
      backend: (viewer) => viewer.distributor.kind,
      createClient: createViewerClient,
      excludeBackendAfterFailure: (backend) => {
        const excluded = backend === "mediamtx" && allowDirectFallback
        if (excluded && isCurrent()) {
          setMediaMTXFallback(true)
          window.dispatchEvent(
            new CustomEvent("rstream:video-distributor-fallback", {
              detail: { from: "mediamtx", to: "direct" },
            }),
          )
        }
        return excluded
      },
      onFailure: fail,
      onPhase: setCurrentPhase,
      onSessionReset: () => {
        playbackMonitor?.stop()
        playbackMonitor = null
        if (isCurrent()) {
          setDistributor(null)
        }
        if (isCurrent() && videoRef.current) {
          videoRef.current.srcObject = null
        }
      },
      onTrack: (event, viewer) => {
        playbackMonitor?.stop()
        playbackMonitor = null
        attachTrack(event, videoRef, isCurrent, setCurrentPhase)
        if (viewer.distributor.kind === "mediamtx" && videoRef.current) {
          playbackMonitor = monitorPlayback(
            videoRef.current,
            event.track.getSettings().frameRate,
            () => {
              const cause = new Error(
                "MediaMTX playback stayed below the usable frame rate",
              )
              controller.recoverCurrentBackend(cause)
            },
          )
        }
      },
      resolve: async (signal, excludedBackend) => {
        const viewer = await fetchViewer(deviceId, signal, excludedBackend)
        allowDirectFallback = viewer.allowDirectFallback
        if (isCurrent()) {
          setDistributor(viewer.distributor.kind)
        }
        return viewer
      },
    })
    void controller.start().catch(fail)
    const onPageHide = () => {
      void controller.stop({ pageHide: true })
      playbackMonitor?.stop()
    }
    const onPageShow = (event: PageTransitionEvent) => {
      if (event.persisted && isCurrent()) setRetryKey((value) => value + 1)
    }
    window.addEventListener("pagehide", onPageHide)
    window.addEventListener("pageshow", onPageShow)
    return () => {
      window.removeEventListener("pagehide", onPageHide)
      window.removeEventListener("pageshow", onPageShow)
      if (isCurrent()) {
        sessionRef.current += 1
      }
      void controller.stop()
      playbackMonitor?.stop()
      if (videoRef.current) {
        videoRef.current.srcObject = null
      }
    }
  }, [deviceId, retryKey])
  async function playCurrentStream() {
    const video = videoRef.current
    if (!video) {
      return
    }
    setPhase("connecting")
    try {
      await playVideo(video)
      setPhase("playing")
    } catch {
      setPhase("blocked")
    }
  }
  return (
    <div
      ref={playerRef}
      className="video-player"
      data-expanded={expanded}
      role={expanded ? "dialog" : undefined}
      aria-modal={expanded ? true : undefined}
      aria-label={expanded ? `${deviceName} — full page video` : undefined}
    >
      <div className="video-player-picture">
        <video
          ref={videoRef}
          className="h-full w-full object-contain"
          playsInline
          muted
          autoPlay
        />
        {phase === "playing" ? null : (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-background">
            {phase === "blocked" ? (
              <Button type="button" size="sm" onClick={playCurrentStream}>
                Play stream
              </Button>
            ) : phase === "error" ? (
              <span className="text-sm text-muted-foreground">
                Unable to start stream
              </span>
            ) : (
              <span className="text-sm text-muted-foreground">
                {phaseLabel(phase)}
              </span>
            )}
          </div>
        )}
      </div>
      <div className="video-player-toolbar">
        <p className="video-player-name" hidden={!expanded} title={deviceName}>
          {deviceName}
        </p>
        <QualitySelector
          key={deviceId}
          deviceId={deviceId}
          compact={expanded}
        />
        <Button
          ref={expandRef}
          type="button"
          variant="outline"
          size="icon"
          className="video-player-expand ml-auto shrink-0"
          aria-label={expanded ? "Exit full page" : "Full page"}
          title={expanded ? "Exit full page (Esc)" : "Full page"}
          aria-expanded={expanded}
          onClick={() => {
            // Capture before the layout change removes the player from normal
            // flow and the browser can clamp the page's scroll offset.
            if (!expanded)
              scrollBeforeExpansion.current = {
                x: window.scrollX,
                y: window.scrollY,
              }
            setExpanded((value) => !value)
          }}
        >
          {expanded ? <Minimize2 aria-hidden /> : <Maximize2 aria-hidden />}
        </Button>
      </div>
      {error ? (
        <div className="video-player-error flex flex-wrap items-center gap-3">
          <p className="text-sm text-destructive">{error}</p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              setError(null)
              setPhase("connecting")
              setRetryKey((current) => current + 1)
            }}
          >
            Retry
          </Button>
        </div>
      ) : null}
      {distributor && !expanded ? (
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground" aria-live="polite">
            Distribution path:{" "}
            <span className="font-medium text-foreground">
              {distributor === "mediamtx" ? "MediaMTX" : "Direct"}
              {distributor === "direct" && mediaMTXFallback
                ? " (MediaMTX fallback)"
                : ""}
            </span>
          </p>
          {distributor === "mediamtx" ? (
            <DistributionMetrics key={deviceId} deviceId={deviceId} />
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

const videoDistributors: Record<
  ViewerPayload["distributor"]["kind"],
  VideoDistributor
> = {
  direct: { allowLegacyWildcardETag: false },
  // MediaMTX 1.20 uses "*" for the initial ETag. This exception stays local to
  // that backend while rstream's producer and the shared client remain strict.
  mediamtx: { allowLegacyWildcardETag: true },
}

function createViewerClient(
  viewer: ViewerPayload,
  callbacks: ViewerClientCallbacks<ViewerPayload, RTCTrackEvent>,
) {
  const distributor = videoDistributors[viewer.distributor.kind]
  return new WHEPClient({
    allowLegacyWildcardETag: distributor.allowLegacyWildcardETag,
    authorization: viewer.distributor.authorization,
    credentialExpiresAt: viewer.distributor.expiresAt,
    endpoint: viewer.distributor.whep,
    iceServers: [
      {
        urls: viewer.turn.urls,
        username: viewer.turn.username,
        credential: viewer.turn.credential,
      },
    ],
    iceCredentialExpiresAt: viewer.turn.expiresAt,
    onClose: (result) => reportWHEPClose(result, viewer.distributor.kind),
    onError: callbacks.onError,
    onTrack: callbacks.onTrack,
    refreshCredentials: async (signal) => {
      const refreshed = await callbacks.refresh(signal)
      return {
        authorization: refreshed.distributor.authorization,
        endpoint: refreshed.distributor.whep,
        expiresAt: refreshed.distributor.expiresAt,
        iceServers: [
          {
            urls: refreshed.turn.urls,
            username: refreshed.turn.username,
            credential: refreshed.turn.credential,
          },
        ],
        iceExpiresAt: refreshed.turn.expiresAt,
      }
    },
  })
}

function reportWHEPClose(
  result: WHEPCloseResult,
  distributor: ViewerPayload["distributor"]["kind"],
) {
  const detail = { ...result, distributor }
  window.dispatchEvent(new CustomEvent("rstream:whep-close", { detail }))
  if (
    result.credentialRefreshFailed ||
    !["already-absent", "deleted", "not-established"].includes(result.outcome)
  ) {
    console.warn("WHEP remote session cleanup was incomplete", detail)
  }
}

function attachTrack(
  event: RTCTrackEvent,
  videoRef: RefObject<HTMLVideoElement | null>,
  isCurrent: () => boolean,
  setPhase: (phase: ViewerPhase) => void,
) {
  if (!isCurrent() || !videoRef.current) {
    return
  }
  const stream = event.streams[0] ?? new MediaStream([event.track])
  const video = videoRef.current
  video.autoplay = true
  video.muted = true
  video.playsInline = true
  video.srcObject = stream
  void playVideo(video)
    .then(() => {
      if (isCurrent()) {
        setPhase("playing")
      }
    })
    .catch(() => {
      if (isCurrent()) {
        setPhase("blocked")
      }
    })
}

async function playVideo(video: HTMLVideoElement) {
  try {
    await video.play()
    return
  } catch {
    await waitForMediaReady(video)
    await video.play()
  }
}

function waitForMediaReady(video: HTMLVideoElement) {
  if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
    return Promise.resolve()
  }
  return new Promise<void>((resolve) => {
    const done = () => {
      window.clearTimeout(timeout)
      video.removeEventListener("loadeddata", done)
      video.removeEventListener("canplay", done)
      resolve()
    }
    const timeout = window.setTimeout(done, 1500)
    video.addEventListener("loadeddata", done, { once: true })
    video.addEventListener("canplay", done, { once: true })
  })
}

async function fetchViewer(
  deviceId: string,
  signal: AbortSignal,
  excludedBackend: string | null,
) {
  const query = excludedBackend === "mediamtx" ? "?distribution=direct" : ""
  const response = await fetch(`/api/devices/${deviceId}/viewer${query}`, {
    method: "POST",
    signal: viewerRequestSignal(signal),
  })
  const body = await responseJSON(response)
  if (!response.ok) {
    throw new Error(apiErrorSchema.parse(body).error)
  }
  return viewerPayloadSchema.parse(body)
}

function monitorPlayback(
  video: HTMLVideoElement,
  expectedFramesPerSecond: number | undefined,
  onUnhealthy: () => void,
) {
  const tracker = new PlaybackHealthTracker({
    minimumFramesPerSecond: minimumUsableFramesPerSecond(
      expectedFramesPerSecond,
    ),
  })
  let stopped = false
  const sample = () => {
    if (stopped) {
      return
    }
    const decodedFrames = decodedVideoFrames(video)
    if (decodedFrames === null) {
      tracker.reset()
      return
    }
    let unhealthy = false
    try {
      unhealthy = tracker.observe({
        active:
          document.visibilityState === "visible" &&
          !video.paused &&
          !video.ended &&
          video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA,
        decodedFrames,
        timeMilliseconds: performance.now(),
      })
    } catch {
      tracker.reset()
      return
    }
    if (unhealthy) {
      stopped = true
      window.clearInterval(timer)
      onUnhealthy()
    }
  }
  const timer = window.setInterval(sample, 1000)
  sample()
  return {
    stop: () => {
      if (stopped) {
        return
      }
      stopped = true
      window.clearInterval(timer)
    },
  }
}

function decodedVideoFrames(video: HTMLVideoElement) {
  let quality: VideoPlaybackQuality | undefined
  try {
    quality = video.getVideoPlaybackQuality?.()
  } catch {
    return null
  }
  if (quality && Number.isFinite(quality.totalVideoFrames)) {
    return quality.totalVideoFrames
  }
  const legacy = video as HTMLVideoElement & {
    webkitDecodedFrameCount?: number
  }
  return Number.isFinite(legacy.webkitDecodedFrameCount)
    ? Number(legacy.webkitDecodedFrameCount)
    : null
}

async function responseJSON(response: Response): Promise<unknown> {
  return response.json()
}

function phaseLabel(
  phase: Exclude<ViewerPhase, "blocked" | "error" | "playing">,
) {
  return phase === "reconnecting" ? "Reconnecting" : "Connecting"
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : "Unable to start playback"
}
