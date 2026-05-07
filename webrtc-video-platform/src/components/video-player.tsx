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

export function VideoPlayer({ deviceId }: { deviceId: string }) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const cleanupRef = useRef<(() => void) | null>(null)
  const [status, setStatus] = useState("Connecting")
  const [error, setError] = useState<string | null>(null)
  const [playing, setPlaying] = useState(false)
  useEffect(() => {
    const fail = (err: unknown) => {
      cleanupRef.current?.()
      cleanupRef.current = null
      setStatus("Idle")
      setPlaying(false)
      setError(errorMessage(err))
    }
    void start(
      deviceId,
      videoRef,
      cleanupRef,
      setStatus,
      setPlaying,
      fail,
    ).catch(fail)
    return () => {
      cleanupRef.current?.()
      cleanupRef.current = null
    }
  }, [deviceId])
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
        {playing ? null : (
          <div className="absolute inset-0 flex items-center justify-center bg-background/80">
            <span className="text-sm text-muted-foreground">{status}</span>
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
              const fail = (err: unknown) => {
                cleanupRef.current?.()
                cleanupRef.current = null
                setStatus("Idle")
                setPlaying(false)
                setError(errorMessage(err))
              }
              setError(null)
              setStatus("Connecting")
              setPlaying(false)
              void start(
                deviceId,
                videoRef,
                cleanupRef,
                setStatus,
                setPlaying,
                fail,
              ).catch(fail)
            }}
          >
            Retry
          </Button>
        </div>
      ) : null}
    </div>
  )
}

async function start(
  deviceId: string,
  videoRef: RefObject<HTMLVideoElement | null>,
  cleanupRef: MutableRefObject<(() => void) | null>,
  setStatus: (status: string) => void,
  setPlaying: (playing: boolean) => void,
  fail: (err: unknown) => void,
) {
  cleanupRef.current?.()
  setStatus("Starting")
  setPlaying(false)
  const viewer = await fetchViewer(deviceId)
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
  let stopped = false
  let offerPending = false
  let restartTimer: number | null = null
  let reconnectTimer: number | null = null
  async function sendLocalOffer({ iceRestart = false } = {}) {
    if (offerPending) {
      return
    }
    if (socket.readyState !== WebSocket.OPEN) {
      throw new Error("Signaling is not open")
    }
    offerPending = true
    try {
      await sendOffer(peer, socket, setStatus, { iceRestart })
    } finally {
      offerPending = false
    }
  }
  function scheduleIceRestart() {
    if (
      restartTimer ||
      peer.signalingState !== "stable" ||
      socket.readyState !== WebSocket.OPEN
    ) {
      return
    }
    setStatus("Reconnecting")
    restartTimer = window.setTimeout(() => {
      restartTimer = null
      void sendLocalOffer({ iceRestart: true }).catch(fail)
    }, 500)
  }
  function scheduleSessionReconnect() {
    if (stopped || reconnectTimer) {
      return
    }
    setStatus("Reconnecting")
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      if (stopped) {
        return
      }
      void start(
        deviceId,
        videoRef,
        cleanupRef,
        setStatus,
        setPlaying,
        fail,
      ).catch(fail)
    }, 1000)
  }
  cleanupRef.current = () => {
    stopped = true
    if (restartTimer) {
      window.clearTimeout(restartTimer)
      restartTimer = null
    }
    if (reconnectTimer) {
      window.clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    socket.close()
    peer.close()
    if (videoRef.current) {
      videoRef.current.srcObject = null
    }
    setPlaying(false)
  }
  peer.addTransceiver("video", { direction: "recvonly" })
  peer.ontrack = (event) => {
    const stream = event.streams[0]
    if (!videoRef.current || !stream) {
      return
    }
    videoRef.current.srcObject = stream
    void videoRef.current
      .play()
      .then(() => {
        setPlaying(true)
        setStatus("Playing")
      })
      .catch(() => setStatus("Paused"))
  }
  peer.onconnectionstatechange = () => {
    switch (peer.connectionState) {
      case "connected":
        if (videoRef.current?.srcObject) {
          setPlaying(true)
          setStatus("Playing")
        }
        break
      case "disconnected":
        scheduleIceRestart()
        break
      case "failed":
        scheduleIceRestart()
        break
      case "closed":
        setStatus("Closed")
        break
      default:
        break
    }
  }
  peer.oniceconnectionstatechange = () => {
    switch (peer.iceConnectionState) {
      case "connected":
      case "completed":
        if (videoRef.current?.srcObject) {
          setPlaying(true)
          setStatus("Playing")
        }
        break
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
    socket.send(
      JSON.stringify({
        type: "webrtc.candidate",
        candidate: event.candidate.candidate,
        sdpMid: event.candidate.sdpMid,
        sdpMLineIndex: event.candidate.sdpMLineIndex,
      }),
    )
  }
  socket.onopen = () => {
    void sendLocalOffer().catch(fail)
  }
  socket.onmessage = (event) => {
    void handleMessage(peer, event.data, setStatus).catch(fail)
  }
  socket.onerror = () => {
    setStatus("Reconnecting")
  }
  socket.onclose = () => {
    scheduleSessionReconnect()
  }
}

async function sendOffer(
  peer: RTCPeerConnection,
  socket: WebSocket,
  setStatus: (status: string) => void,
  options: { iceRestart?: boolean } = {},
) {
  setStatus(options.iceRestart ? "Reconnecting" : "Connecting")
  // ICE restart keeps the same viewer session and asks both peers to gather
  // fresh candidates after an IP address or network interface change.
  const offer = await peer.createOffer(
    options.iceRestart ? { iceRestart: true } : undefined,
  )
  await peer.setLocalDescription(offer)
  socket.send(
    JSON.stringify({
      type: "webrtc.offer",
      sdp: offer.sdp,
    }),
  )
}

async function handleMessage(
  peer: RTCPeerConnection,
  data: unknown,
  setStatus: (status: string) => void,
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
      setStatus("Connecting")
      break
    case "webrtc.candidate":
      if (!parsed.data.candidate) {
        return
      }
      await peer.addIceCandidate({
        candidate: parsed.data.candidate,
        sdpMid: parsed.data.sdpMid ?? null,
        sdpMLineIndex: parsed.data.sdpMLineIndex,
      })
      break
    case "error":
      throw new Error(parsed.data.message ?? "Producer returned an error")
    default:
      break
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

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : "Unable to start playback"
}
