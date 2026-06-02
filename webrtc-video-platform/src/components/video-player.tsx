"use client"

import { type MutableRefObject } from "react"
import { type RefObject } from "react"
import { useEffect } from "react"
import { useRef } from "react"
import { useState } from "react"

import { apiErrorSchema } from "@/lib/validations/device"
import { Button } from "@/components/ui/button"
import { signalMessageSchema } from "@/lib/validations/device"
import { viewerPayloadSchema } from "@/lib/validations/device"

type PlayerPhase =
  | "connecting"
  | "reconnecting"
  | "blocked"
  | "error"
  | "playing"

type ViewerSessionOptions = {
  cleanupRef: MutableRefObject<(() => void) | null>
  deviceId: string
  fail: (err: unknown) => void
  isCurrent: () => boolean
  reconnectAttempt: number
  setPhase: (phase: PlayerPhase) => void
  videoRef: RefObject<HTMLVideoElement | null>
}

// WebRTC callbacks need current mutable flags without triggering React renders.
type ViewerSessionState = {
  offerPending: boolean
  reconnectTimer: number | null
  restartTimer: number | null
  stopped: boolean
}

const maxSessionReconnects = 5
const maxPendingRemoteCandidates = 64
const sessionReconnectBaseDelayMs = 1000
const sessionReconnectJitterMs = 250
const sessionReconnectMaxDelayMs = 15000

// sessionRef guards against React Strict Mode remounts and stale callbacks.
export function VideoPlayer({ deviceId }: { deviceId: string }) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const cleanupRef = useRef<(() => void) | null>(null)
  const sessionRef = useRef(0)
  const [phase, setPhase] = useState<PlayerPhase>("connecting")
  const [error, setError] = useState<string | null>(null)
  const [retryKey, setRetryKey] = useState(0)
  useEffect(() => {
    const session = sessionRef.current + 1
    sessionRef.current = session
    const isCurrent = () => sessionRef.current === session
    const setCurrentPhase = (nextPhase: PlayerPhase) => {
      if (isCurrent()) {
        setPhase(nextPhase)
      }
    }
    const fail = (err: unknown) => {
      if (!isCurrent()) {
        return
      }
      cleanupRef.current?.()
      cleanupRef.current = null
      setPhase("error")
      setError(errorMessage(err))
    }
    setError(null)
    setPhase("connecting")
    void startViewerSession({
      cleanupRef,
      deviceId,
      fail,
      isCurrent,
      reconnectAttempt: 0,
      setPhase: setCurrentPhase,
      videoRef,
    }).catch(fail)
    return () => {
      if (isCurrent()) {
        sessionRef.current += 1
      }
      cleanupRef.current?.()
      cleanupRef.current = null
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
    <div className="space-y-3">
      <div className="relative aspect-video overflow-hidden rounded-lg border border-foreground/20 bg-background">
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
      {error ? (
        <div className="flex flex-wrap items-center gap-3">
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
    </div>
  )
}

// Trickle ICE can race SDP exchange, so candidates may need short queues.
async function startViewerSession({
  cleanupRef,
  deviceId,
  fail,
  isCurrent,
  reconnectAttempt,
  setPhase,
  videoRef,
}: ViewerSessionOptions) {
  cleanupRef.current?.()
  cleanupRef.current = null
  setPhase("connecting")
  const viewer = await fetchViewer(deviceId)
  if (!isCurrent()) {
    return
  }
  const peer = new RTCPeerConnection({
    iceServers: [
      {
        urls: viewer.turn.urls,
        username: viewer.turn.username,
        credential: viewer.turn.credential,
      },
    ],
  })
  const socket = new WebSocket(viewer.endpoints.ws)
  const sessionState: ViewerSessionState = {
    offerPending: false,
    reconnectTimer: null,
    restartTimer: null,
    stopped: false,
  }
  const pendingRemoteCandidates: RTCIceCandidateInit[] = []
  const pendingLocalCandidates: RTCIceCandidateInit[] = []
  const options = {
    cleanupRef,
    deviceId,
    fail,
    isCurrent,
    reconnectAttempt,
    setPhase,
    videoRef,
  }
  async function sendLocalOffer({ iceRestart = false } = {}) {
    if (sessionState.offerPending || !isCurrent()) {
      return
    }
    if (socket.readyState !== WebSocket.OPEN) {
      throw new Error("Signaling is not open")
    }
    sessionState.offerPending = true
    try {
      const offer = await peer.createOffer(
        iceRestart ? { iceRestart: true } : undefined,
      )
      await peer.setLocalDescription(offer)
      socket.send(
        JSON.stringify({
          type: "webrtc.offer",
          sdp: offer.sdp,
        }),
      )
      flushLocalCandidates()
    } finally {
      sessionState.offerPending = false
    }
  }
  function sendLocalCandidate(candidate: RTCIceCandidateInit) {
    if (socket.readyState !== WebSocket.OPEN) {
      return
    }
    socket.send(
      JSON.stringify({
        type: "webrtc.candidate",
        ...candidate,
      }),
    )
  }
  function flushLocalCandidates() {
    while (pendingLocalCandidates.length > 0) {
      const candidate = pendingLocalCandidates.shift()
      if (candidate) {
        sendLocalCandidate(candidate)
      }
    }
  }
  function scheduleIceRestart() {
    if (
      sessionState.restartTimer ||
      peer.signalingState !== "stable" ||
      socket.readyState !== WebSocket.OPEN ||
      !isCurrent()
    ) {
      return
    }
    setPhase("reconnecting")
    sessionState.restartTimer = window.setTimeout(() => {
      sessionState.restartTimer = null
      if (!isCurrent()) {
        return
      }
      void sendLocalOffer({ iceRestart: true }).catch(fail)
    }, 500)
  }
  function scheduleSessionReconnect() {
    if (sessionState.stopped || sessionState.reconnectTimer || !isCurrent()) {
      return
    }
    if (options.reconnectAttempt >= maxSessionReconnects) {
      fail(new Error("Viewer reconnect limit reached."))
      return
    }
    const nextAttempt = options.reconnectAttempt + 1
    setPhase("reconnecting")
    sessionState.reconnectTimer = window.setTimeout(() => {
      sessionState.reconnectTimer = null
      if (sessionState.stopped || !isCurrent()) {
        return
      }
      void startViewerSession({
        ...options,
        reconnectAttempt: nextAttempt,
      }).catch(fail)
    }, sessionReconnectDelay(nextAttempt))
  }
  cleanupRef.current = () => {
    sessionState.stopped = true
    if (sessionState.restartTimer) {
      window.clearTimeout(sessionState.restartTimer)
      sessionState.restartTimer = null
    }
    if (sessionState.reconnectTimer) {
      window.clearTimeout(sessionState.reconnectTimer)
      sessionState.reconnectTimer = null
    }
    socket.close()
    peer.close()
    if (videoRef.current) {
      videoRef.current.srcObject = null
    }
  }
  peer.addTransceiver("video", { direction: "recvonly" })
  peer.ontrack = (event) => {
    const stream = event.streams[0]
    if (!isCurrent() || !videoRef.current || !stream) {
      return
    }
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
  peer.onconnectionstatechange = () => {
    switch (peer.connectionState) {
      case "disconnected":
      case "failed":
        scheduleIceRestart()
        break
      default:
        break
    }
  }
  peer.oniceconnectionstatechange = () => {
    switch (peer.iceConnectionState) {
      case "disconnected":
      case "failed":
        scheduleIceRestart()
        break
      default:
        break
    }
  }
  peer.onicecandidate = (event) => {
    if (!event.candidate || socket.readyState !== WebSocket.OPEN) {
      return
    }
    const candidate = event.candidate.toJSON()
    if (sessionState.offerPending) {
      pendingLocalCandidates.push(candidate)
      return
    }
    sendLocalCandidate(candidate)
  }
  socket.onopen = () => {
    void sendLocalOffer().catch(fail)
  }
  socket.onmessage = (event) => {
    void handleMessage(peer, event.data, pendingRemoteCandidates).catch(fail)
  }
  socket.onerror = () => {
    if (isCurrent()) {
      setPhase("reconnecting")
    }
  }
  socket.onclose = () => {
    scheduleSessionReconnect()
  }
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

function sessionReconnectDelay(attempt: number) {
  const exponentialDelay = sessionReconnectBaseDelayMs * 2 ** (attempt - 1)
  const boundedDelay = Math.min(exponentialDelay, sessionReconnectMaxDelayMs)
  const jitter = Math.floor(Math.random() * sessionReconnectJitterMs)
  return boundedDelay + jitter
}

async function handleMessage(
  peer: RTCPeerConnection,
  data: unknown,
  pendingRemoteCandidates: RTCIceCandidateInit[],
) {
  const parsed = signalMessageSchema.safeParse(JSON.parse(String(data)))
  if (!parsed.success) {
    throw new Error("Unexpected signaling message")
  }
  switch (parsed.data.type) {
    case "webrtc.answer":
      await peer.setRemoteDescription({
        type: "answer",
        sdp: parsed.data.sdp,
      })
      await flushRemoteCandidates(peer, pendingRemoteCandidates)
      break
    case "webrtc.candidate":
      if (!parsed.data.candidate) {
        return
      }
      await addRemoteCandidate(peer, pendingRemoteCandidates, {
        candidate: parsed.data.candidate,
        sdpMid: parsed.data.sdpMid ?? null,
        sdpMLineIndex: parsed.data.sdpMLineIndex,
        usernameFragment: parsed.data.usernameFragment ?? null,
      })
      break
    case "error":
      throw new Error(parsed.data.message ?? "Producer returned an error")
    default:
      break
  }
}

async function addRemoteCandidate(
  peer: RTCPeerConnection,
  pendingRemoteCandidates: RTCIceCandidateInit[],
  candidate: RTCIceCandidateInit,
) {
  if (!peer.remoteDescription) {
    if (pendingRemoteCandidates.length >= maxPendingRemoteCandidates) {
      throw new Error("Too many pending remote ICE candidates")
    }
    pendingRemoteCandidates.push(candidate)
    return
  }
  await peer.addIceCandidate(candidate)
}

async function flushRemoteCandidates(
  peer: RTCPeerConnection,
  pendingRemoteCandidates: RTCIceCandidateInit[],
) {
  while (pendingRemoteCandidates.length > 0) {
    const candidate = pendingRemoteCandidates.shift()
    if (candidate) {
      await peer.addIceCandidate(candidate)
    }
  }
}

async function fetchViewer(deviceId: string) {
  const response = await fetch(`/api/devices/${deviceId}/viewer`, {
    method: "POST",
  })
  const body = await responseJSON(response)
  if (!response.ok) {
    throw new Error(apiErrorSchema.parse(body).error)
  }
  return viewerPayloadSchema.parse(body)
}

async function responseJSON(response: Response): Promise<unknown> {
  return response.json()
}

function phaseLabel(
  phase: Exclude<PlayerPhase, "blocked" | "error" | "playing">,
) {
  return phase === "reconnecting" ? "Reconnecting" : "Connecting"
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : "Unable to start playback"
}
