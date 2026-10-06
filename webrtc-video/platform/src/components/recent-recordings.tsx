"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Pause, Play } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  latestRecordingClip,
  readRecordingWindow,
  recordingClipURL,
  selectRecordingClip,
  type RecordingClip,
  type RecordingWindow,
} from "@/lib/recording-timeline"

type Selection = RecordingClip & { sequence: number }
type Continuation = { at: number; since: number }

export function useRecentRecordings(deviceId: string) {
  const video = useRef<HTMLVideoElement>(null)
  const [available, setAvailable] = useState(false)
  const [active, setActive] = useState(false)
  const [window, setWindow] = useState<RecordingWindow | null>(null)
  const latest = useRef<RecordingWindow | null>(null)
  const [indexError, setIndexError] = useState(false)
  const [selection, setSelection] = useState<Selection | null>(null)
  const [position, setPosition] = useState(0)
  const positionRef = useRef(0)
  const [playing, setPlaying] = useState(false)
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const [visible, setVisible] = useState(true)
  const intent = useRef(false)
  const sequence = useRef(0)
  const continuation = useRef<Continuation | null>(null)
  const refreshRef = useRef<() => void>(() => {})
  const playRef = useRef<() => void>(() => {})

  const load = useCallback((clip: RecordingClip, autoplay = true) => {
    continuation.current = null
    intent.current = autoplay
    positionRef.current = clip.start
    setPosition(clip.start)
    setMessage(null)
    setPlaying(false)
    setLoading(true)
    setSelection({ ...clip, sequence: ++sequence.current })
  }, [])

  const seek = useCallback(
    (at: number) => {
      const clip = latest.current && selectRecordingClip(latest.current, at)
      if (clip) {
        load(clip)
        return
      }
      continuation.current = null
      intent.current = false
      setSelection(null)
      setPlaying(false)
      setLoading(false)
      positionRef.current = at
      setPosition(at)
      setMessage(
        "No recording at this time. Choose another point or return to live.",
      )
      refreshRef.current()
    },
    [load],
  )

  const advance = useCallback(
    (index: RecordingWindow) => {
      const pending = continuation.current
      if (!pending) return
      const clip = selectRecordingClip(index, pending.at)
      if (clip) {
        load(clip)
        return
      }
      if (
        index.spans.some((span) => span.start > pending.at) ||
        performance.now() - pending.since >= 12000
      ) {
        continuation.current = null
        setLoading(false)
        setMessage(
          "End of this recording. Choose another point or return to live.",
        )
      }
    },
    [load],
  )

  const close = useCallback(() => {
    continuation.current = null
    intent.current = false
    setActive(false)
    setSelection(null)
    positionRef.current = 0
    setPosition(0)
    setPlaying(false)
    setLoading(false)
    setMessage(null)
  }, [])

  useEffect(() => {
    let disposed = false,
      disabled = false,
      refreshAfter = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let current: AbortController | undefined
    setAvailable(false)
    setWindow(null)
    latest.current = null
    setIndexError(false)
    close()
    async function refresh() {
      clearTimeout(timer)
      if (disposed || disabled || document.hidden) return
      if (current) {
        refreshAfter = true
        return
      }
      const controller = new AbortController()
      current = controller
      const deadline = setTimeout(() => controller.abort(), 5000)
      try {
        const response = await fetch(
          `/api/devices/${encodeURIComponent(deviceId)}/recordings`,
          {
            signal: controller.signal,
            cache: "no-store",
            redirect: "error",
          },
        )
        if ([204, 401, 403, 404].includes(response.status)) {
          await response.body?.cancel()
          disabled = true
          if (!disposed) {
            setAvailable(false)
            close()
          }
          return
        }
        if (!response.ok) {
          await response.body?.cancel()
          throw new Error("Unavailable")
        }
        const index = await readRecordingWindow(response, controller.signal)
        if (disposed || document.hidden) return
        latest.current = index
        setWindow(index)
        setAvailable(true)
        setIndexError(false)
        advance(index)
      } catch {
        if (!disposed && !document.hidden) {
          latest.current = null
          setIndexError(true)
          if (continuation.current) {
            continuation.current = null
            setLoading(false)
            setMessage(
              "Recent recordings are unavailable. Try another point after the list recovers.",
            )
          }
        }
      } finally {
        clearTimeout(deadline)
        current = undefined
        if (!disposed && !disabled && !document.hidden) {
          timer = setTimeout(refresh, refreshAfter ? 0 : 5000)
          refreshAfter = false
        }
      }
    }
    const onVisibility = () => {
      setVisible(!document.hidden)
      clearTimeout(timer)
      if (document.hidden) {
        current?.abort()
        continuation.current = null
        intent.current = false
        setPlaying(false)
        // Preserve the playback position, cancel the old media load in the
        // effect below, and resume paused if the recording is still available.
        setSelection(
          (previous) =>
            previous && {
              ...previous,
              start: Math.max(
                previous.start,
                Math.min(positionRef.current, previous.end - 250),
              ),
              sequence: ++sequence.current,
            },
        )
      } else void refresh()
    }
    refreshRef.current = () => {
      void refresh()
    }
    document.addEventListener("visibilitychange", onVisibility)
    globalThis.window.addEventListener("pagehide", close)
    globalThis.window.addEventListener("pageshow", onVisibility)
    onVisibility()
    return () => {
      disposed = true
      clearTimeout(timer)
      current?.abort()
      refreshRef.current = () => {}
      document.removeEventListener("visibilitychange", onVisibility)
      globalThis.window.removeEventListener("pagehide", close)
      globalThis.window.removeEventListener("pageshow", onVisibility)
    }
  }, [deviceId, advance, close])

  useEffect(() => {
    const element = video.current
    if (!active || !visible || !selection || !element) return
    let disposed = false,
      failed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const fail = () => {
      if (disposed || failed) return
      failed = true
      clearTimeout(timer)
      intent.current = false
      continuation.current = null
      setLoading(false)
      setPlaying(false)
      setMessage(
        "This recording could not be loaded or has expired. Choose another point.",
      )
      element.pause()
      element.removeAttribute("src")
      element.load()
      refreshRef.current()
    }
    const waiting = () => {
      if (disposed || failed) return
      clearTimeout(timer)
      setLoading(true)
      timer = setTimeout(fail, 35000)
    }
    const onPlaying = () => {
      if (!disposed && !failed) {
        clearTimeout(timer)
        setLoading(false)
        setPlaying(true)
      }
    }
    const onPause = () => {
      if (!disposed) setPlaying(false)
    }
    const onTime = () => {
      if (disposed || failed) return
      positionRef.current = Math.min(
        selection.end,
        selection.start + element.currentTime * 1000,
      )
      setPosition(positionRef.current)
    }
    const onReady = () => {
      if (disposed || failed) return
      clearTimeout(timer)
      setLoading(false)
      if (intent.current)
        void element.play().catch(() => {
          if (!disposed && !failed) {
            intent.current = false
            setPlaying(false)
          }
        })
    }
    const onEnded = () => {
      if (disposed || failed) return
      clearTimeout(timer)
      setPlaying(false)
      intent.current = false
      const duration = Math.min(
        element.duration * 1000,
        selection.end - selection.start,
      )
      if (!Number.isFinite(duration) || duration < 250) {
        fail()
        return
      }
      continuation.current = {
        at: Math.round(selection.start + duration),
        since: performance.now(),
      }
      setLoading(true)
      if (latest.current) advance(latest.current)
      refreshRef.current()
    }
    const togglePlay = () => {
      if (disposed || failed) return
      if (intent.current || !element.paused) {
        intent.current = false
        continuation.current = null
        element.pause()
      } else if (element.ended) seek(positionRef.current)
      else {
        intent.current = true
        void element.play().catch(() => {
          if (!disposed && !failed) {
            intent.current = false
            setPlaying(false)
          }
        })
      }
    }
    playRef.current = togglePlay
    const handlers = {
      error: fail,
      waiting,
      playing: onPlaying,
      pause: onPause,
      loadeddata: onReady,
      timeupdate: onTime,
      ended: onEnded,
    }
    for (const [event, handler] of Object.entries(handlers))
      element.addEventListener(event, handler)
    element.src = recordingClipURL(deviceId, selection)
    element.load()
    waiting()
    return () => {
      disposed = true
      if (playRef.current === togglePlay) playRef.current = () => {}
      clearTimeout(timer)
      for (const [event, handler] of Object.entries(handlers))
        element.removeEventListener(event, handler)
      element.pause()
      element.removeAttribute("src")
      element.load() // Abort the previous HTTP media body on seek/close/unmount.
    }
  }, [deviceId, selection, active, visible, advance, seek])

  return {
    active,
    available,
    window,
    indexError,
    video,
    position,
    playing,
    loading,
    message,
    seek,
    toggle: () => {
      if (active) {
        close()
        return
      }
      setActive(true)
      const clip = latest.current && latestRecordingClip(latest.current)
      if (clip) load(clip)
      else {
        setMessage(
          indexError
            ? "Recent recordings are unavailable. Try another point after the list recovers."
            : "No recent recordings yet. Recordings appear while the source is streaming.",
        )
        refreshRef.current()
      }
    },
    togglePlay: () => playRef.current(),
    canPlay: !!selection && !message && !loading,
  }
}

type Playback = ReturnType<typeof useRecentRecordings>
const time = (at: number) =>
  new Date(at).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  })

export function RecordingPicture({ playback }: { playback: Playback }) {
  if (!playback.active) return null
  return (
    <div className="recording-picture absolute inset-0 bg-black">
      <video
        ref={playback.video}
        className="h-full w-full object-contain"
        muted
        playsInline
        aria-label="Recorded video"
      />
      {playback.loading || playback.message ? (
        <div
          className="absolute inset-0 flex items-center justify-center bg-background/90 p-4 text-center text-sm text-muted-foreground"
          role="status"
        >
          {playback.message ?? "Loading recording…"}
        </div>
      ) : null}
    </div>
  )
}

export function RecordingTransport({ playback }: { playback: Playback }) {
  if (!playback.active) return null
  return (
    <div className="recording-transport flex min-w-0 items-center gap-2">
      <Button
        type="button"
        variant="outline"
        size="icon"
        disabled={!playback.canPlay}
        onClick={playback.togglePlay}
        aria-label={playback.playing ? "Pause recording" : "Play recording"}
      >
        {playback.playing ? <Pause aria-hidden /> : <Play aria-hidden />}
      </Button>
      <p className="flex min-w-0 items-center gap-2 text-xs">
        <span className="hidden text-muted-foreground sm:inline">Recorded</span>
        <span className="truncate tabular-nums">
          {playback.position ? time(playback.position) : "—"}
        </span>
      </p>
    </div>
  )
}

export function RecordingTimeline({ playback }: { playback: Playback }) {
  const [draft, setDraft] = useState<{
    at: number
    window: RecordingWindow
  } | null>(null)
  const dragging = useRef(false)
  const draftRef = useRef<typeof draft>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  if (!playback.active || !playback.window) return null
  const window = draft?.window ?? playback.window
  const duration = window.end - window.start
  const at = Math.min(
    window.end,
    Math.max(window.start, draft?.at ?? (playback.position || window.end)),
  )
  const commit = (at: number) => {
    clearTimeout(timer.current)
    draftRef.current = null
    setDraft(null)
    playback.seek(at)
  }
  return (
    <div className="recording-timeline grid gap-2">
      <div className="relative flex h-6 items-center">
        <div
          className="pointer-events-none absolute inset-x-0 h-1 overflow-hidden rounded-full bg-border"
          aria-hidden
        >
          {window.spans.map((span) => (
            <span
              key={span.start}
              className="absolute h-full bg-foreground/70"
              style={{
                left: `${(100 * (span.start - window.start)) / duration}%`,
                width: `${(100 * (span.end - span.start)) / duration}%`,
              }}
            />
          ))}
        </div>
        <input
          type="range"
          className="recording-seek relative h-6 w-full cursor-pointer accent-foreground disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Recording time"
          title="Dark segments contain recordings. Gaps have no recording."
          aria-valuetext={time(at)}
          min={window.start}
          max={window.end}
          step={1000}
          value={at}
          disabled={playback.indexError || !window.spans.length}
          onPointerDown={(event) => {
            dragging.current = true
            event.currentTarget.setPointerCapture(event.pointerId)
          }}
          onPointerUp={(event) => {
            dragging.current = false
            commit(Number(event.currentTarget.value))
          }}
          onPointerCancel={() => {
            dragging.current = false
            clearTimeout(timer.current)
            draftRef.current = null
            setDraft(null)
          }}
          onBlur={() => {
            dragging.current = false
            if (draftRef.current) commit(draftRef.current.at)
          }}
          onChange={(event) => {
            const value = Number(event.target.value)
            clearTimeout(timer.current)
            draftRef.current = { at: value, window }
            setDraft(draftRef.current)
            if (!dragging.current)
              timer.current = setTimeout(() => commit(value), 250)
          }}
        />
      </div>
      <div className="flex justify-between text-xs text-muted-foreground">
        <span>{time(window.start)}</span>
        <span>{time(window.end)}</span>
      </div>
      {playback.indexError ? (
        <p role="status" className="text-xs text-muted-foreground">
          Recording list unavailable.
        </p>
      ) : null}
    </div>
  )
}
