// Browser-side recording bounds. Server time anchors the window; monotonic
// elapsed time ages it without assuming the viewer's wall clock is accurate.
export type RecordingWindow = {
  start: number
  end: number
  receivedAt: number
  maximumClipSeconds: number
  spans: { start: number; end: number }[]
}
export type RecordingClip = { start: number; end: number }

export function parseRecordingWindow(
  value: unknown,
  receivedAt = performance.now(),
): RecordingWindow {
  if (!value || typeof value !== "object") throw new Error("Invalid index")
  const v = value as Record<string, unknown>
  const date = (raw: unknown) => {
    if (typeof raw !== "string" || raw.length > 40) return NaN
    return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(raw)
      ? Date.parse(raw)
      : NaN
  }
  const start = date(v.windowStart),
    end = date(v.windowEnd)
  if (
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    end - start < 30000 ||
    end - start > 600000 ||
    typeof v.maximumClipSeconds !== "number" ||
    !Number.isFinite(v.maximumClipSeconds) ||
    v.maximumClipSeconds < 1 ||
    v.maximumClipSeconds > 30 ||
    !Array.isArray(v.spans) ||
    v.spans.length > 256
  )
    throw new Error("Invalid recording window")
  let previous = start
  const spans = v.spans.map((raw) => {
    if (!raw || typeof raw !== "object") throw new Error("Invalid span")
    const span = { start: date(raw.start), end: date(raw.end) }
    if (
      !Number.isFinite(span.start) ||
      !Number.isFinite(span.end) ||
      span.start < previous ||
      span.end <= span.start ||
      span.end > end
    )
      throw new Error("Invalid recording span")
    previous = span.end
    return span
  })
  return {
    start,
    end,
    receivedAt,
    spans,
    maximumClipSeconds: v.maximumClipSeconds,
  }
}

export function selectRecordingClip(
  window: RecordingWindow,
  requested: number,
  now = performance.now(),
): RecordingClip | null {
  // Leave one second for a request to reach the server at the retention edge.
  const earliest = window.start + Math.max(0, now - window.receivedAt) + 1000
  const start = Math.round(requested)
  if (!Number.isFinite(start) || start < earliest) return null
  const span = window.spans.find(
    (span) => span.start <= start && start < span.end,
  )
  if (!span) return null
  const end = Math.min(span.end, start + window.maximumClipSeconds * 1000)
  return end - start >= 250 ? { start, end } : null
}

export function latestRecordingClip(
  window: RecordingWindow,
  now = performance.now(),
) {
  for (let i = window.spans.length - 1; i >= 0; i--) {
    const span = window.spans[i]
    const start = Math.max(
      span.start,
      span.end - 10000,
      window.start + Math.max(0, now - window.receivedAt) + 1000,
    )
    const clip = selectRecordingClip(window, start, now)
    if (clip) return clip
  }
  return null
}

export function recordingClipURL(deviceId: string, clip: RecordingClip) {
  return `/api/devices/${encodeURIComponent(deviceId)}/recordings/playback?${new URLSearchParams(
    {
      start: new Date(clip.start).toISOString(),
      duration: ((clip.end - clip.start) / 1000).toFixed(3),
    },
  )}`
}

export async function readRecordingWindow(
  response: Response,
  signal: AbortSignal,
) {
  if (!response.body) throw new Error("Missing index")
  const reader = response.body.getReader()
  const decoder = new TextDecoder("utf-8", { fatal: true })
  const abort = () => {
    void reader.cancel().catch(() => {})
  }
  signal.addEventListener("abort", abort, { once: true })
  let length = 0,
    text = ""
  try {
    while (true) {
      signal.throwIfAborted()
      const { value, done } = await reader.read()
      signal.throwIfAborted()
      if (done) break
      length += value.length
      if (length > 64 * 1024) throw new Error("Index too large")
      text += decoder.decode(value, { stream: true })
    }
    text += decoder.decode()
    return parseRecordingWindow(JSON.parse(text))
  } finally {
    signal.removeEventListener("abort", abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
