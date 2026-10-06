// Private MediaMTX playback client. Callers must authorize the device before
// every operation, including cached index reads. Upstream URLs and credentials
// never form part of the returned index or media response.
export type RecordingSpan = { start: string; end: string }
export type RecordingIndex = {
  windowStart: string
  windowEnd: string
  spans: RecordingSpan[]
  maximumClipSeconds: number
}

type Entry = { expires: number; result?: RecordingIndex }
type Job = {
  controller: AbortController
  promise: Promise<RecordingIndex>
  waiters: number
}

const maximumClipSeconds = 30
const maximumMediaBytes = 64 * 1024 * 1024
const maximumIndexBytes = 64 * 1024
const maximumEntries = 128
const cacheMilliseconds = 2000
const pathPattern =
  /^devices\/[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const unavailable = () => new Error("Recording service is unavailable")

export class RecordingNotFound extends Error {
  constructor() {
    super("Recording is no longer available")
  }
}

export function validatePlaybackURL(raw: string) {
  const url = new URL(raw)
  if (
    raw.length > 2048 ||
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw new Error(
      "Playback URL must be an HTTP(S) base URL without credentials, query or fragment",
    )
  if (!url.pathname.endsWith("/")) url.pathname += "/"
  return url
}

export function recordingClip(
  query: URLSearchParams,
  now: number,
  windowSeconds: number,
) {
  if (
    [...query.keys()].some((key) => key !== "start" && key !== "duration") ||
    query.getAll("start").length !== 1 ||
    query.getAll("duration").length !== 1
  )
    throw new RangeError("Specify one recording start and duration")
  const start = timestamp(query.get("start"))
  const rawDuration = query.get("duration")!
  const duration = /^\d+(?:\.\d{1,3})?$/.test(rawDuration)
    ? Number(rawDuration)
    : NaN
  if (
    start === null ||
    !Number.isFinite(duration) ||
    duration <= 0 ||
    duration > maximumClipSeconds ||
    start < now - windowSeconds * 1000 ||
    start >= now ||
    start + duration * 1000 > now
  )
    throw new RangeError(
      "Recording must be within the recent window and at most 30 seconds long",
    )
  return { start: new Date(start).toISOString(), duration }
}

export class MediaMTXPlayback {
  private readonly endpoint: URL
  private readonly fetcher: typeof fetch
  private readonly credential: (path: string) => string
  private readonly windowSeconds: number
  private readonly entries = new Map<string, Entry>()
  private readonly jobs = new Map<string, Job>()
  private clips = 0

  constructor(options: {
    endpoint: string
    credential: (path: string) => string
    windowSeconds: number
    fetch?: typeof fetch
  }) {
    this.endpoint = validatePlaybackURL(options.endpoint)
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis)
    this.credential = options.credential
    if (
      !Number.isSafeInteger(options.windowSeconds) ||
      options.windowSeconds < 30 ||
      options.windowSeconds > 600
    )
      throw new Error("Recording window must be from 30 through 600 seconds")
    this.windowSeconds = options.windowSeconds
  }

  async index(path: string, signal: AbortSignal): Promise<RecordingIndex> {
    this.check(path, signal)
    const cached = this.entries.get(path)
    if (cached && performance.now() < cached.expires) {
      if (!cached.result) throw unavailable()
      return structuredClone(cached.result)
    }
    let job = this.jobs.get(path)
    if (!job) {
      if (this.jobs.size >= 4) throw unavailable()
      const controller = new AbortController()
      const promise = this.readIndex(path, controller.signal)
        .then(
          (result) => {
            this.remember(path, result)
            return result
          },
          () => {
            if (!controller.signal.aborted) this.remember(path)
            throw unavailable()
          },
        )
        .finally(() => this.jobs.delete(path))
      job = { controller, promise, waiters: 0 }
      this.jobs.set(path, job)
    }
    if (job.controller.signal.aborted || job.waiters >= 32) throw unavailable()
    return this.subscribe(job, signal)
  }

  async clip(
    path: string,
    query: URLSearchParams,
    signal: AbortSignal,
  ): Promise<Response> {
    this.check(path, signal)
    const { start, duration } = recordingClip(
      query,
      Date.now(),
      this.windowSeconds,
    )
    if (this.clips >= 4) throw unavailable()
    this.clips++
    let released = false
    const release = () => {
      if (!released) {
        released = true
        this.clips--
      }
    }
    try {
      const url = new URL("get", this.endpoint)
      url.search = new URLSearchParams({
        path,
        start,
        duration: String(duration),
        format: "mp4",
      }).toString()
      const body = await this.open(
        url,
        path,
        signal,
        30_000,
        maximumMediaBytes,
        "video/mp4",
        release,
      )
      return new Response(body, {
        headers: {
          "Content-Type": "video/mp4",
          "Cache-Control": "private, no-store",
          "Accept-Ranges": "none",
          "Content-Disposition": 'inline; filename="recording.mp4"',
          "Cross-Origin-Resource-Policy": "same-origin",
          "X-Content-Type-Options": "nosniff",
        },
      })
    } catch (error) {
      release()
      throw error
    }
  }

  private check(path: string, signal: AbortSignal) {
    signal.throwIfAborted()
    if (!pathPattern.test(path)) throw unavailable()
  }

  private remember(path: string, result?: RecordingIndex) {
    this.entries.delete(path)
    if (this.entries.size >= maximumEntries)
      this.entries.delete(this.entries.keys().next().value!)
    this.entries.set(path, {
      expires: performance.now() + cacheMilliseconds,
      result,
    })
  }

  private subscribe(job: Job, signal: AbortSignal): Promise<RecordingIndex> {
    job.waiters++
    return new Promise((resolve, reject) => {
      let settled = false
      const finish = (error?: unknown, result?: RecordingIndex) => {
        if (settled) return
        settled = true
        signal.removeEventListener("abort", abort)
        if (--job.waiters === 0) job.controller.abort()
        if (error) reject(error)
        else resolve(structuredClone(result!))
      }
      const abort = () => finish(signal.reason ?? unavailable())
      signal.addEventListener("abort", abort, { once: true })
      if (signal.aborted) abort()
      void job.promise.then(
        (value) => finish(undefined, value),
        (error) => finish(error),
      )
    })
  }

  private async readIndex(
    path: string,
    signal: AbortSignal,
  ): Promise<RecordingIndex> {
    const end = Date.now()
    const start = end - this.windowSeconds * 1000
    const windowStart = new Date(start).toISOString()
    const windowEnd = new Date(end).toISOString()
    const url = new URL("list", this.endpoint)
    url.search = new URLSearchParams({
      path,
      start: windowStart,
      end: windowEnd,
    }).toString()
    let spans: RecordingSpan[]
    try {
      const stream = await this.open(
        url,
        path,
        signal,
        5000,
        maximumIndexBytes,
        "application/json",
      )
      spans = parseRecordingSpans(await new Response(stream).json(), start, end)
    } catch (error) {
      if (!(error instanceof RecordingNotFound)) throw error
      spans = []
    }
    signal.throwIfAborted()
    return { windowStart, windowEnd, spans, maximumClipSeconds }
  }

  // The deadline and admission slot span both headers and body consumption.
  // Backpressure keeps the media body streaming; disconnect/timeout explicitly
  // cancels its reader even when the downstream stopped pulling altogether.
  private async open(
    url: URL,
    path: string,
    cancellation: AbortSignal,
    timeout: number,
    maximumBytes: number,
    contentType: string,
    release = () => {},
  ): Promise<ReadableStream<Uint8Array>> {
    const controller = new AbortController()
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
    let output: ReadableStreamDefaultController<Uint8Array> | undefined
    let settled = false
    const finish = () => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      cancellation.removeEventListener("abort", abort)
      controller.abort()
      void reader?.cancel().catch(() => undefined)
      release()
    }
    const abort = () => {
      output?.error(unavailable())
      finish()
    }
    const timer = setTimeout(abort, timeout)
    cancellation.addEventListener("abort", abort, { once: true })
    if (cancellation.aborted) abort()
    try {
      const response = await this.fetcher(url, {
        signal: controller.signal,
        redirect: "error",
        cache: "no-store",
        credentials: "omit",
        headers: {
          Authorization: `Bearer ${this.credential(path)}`,
          Accept: contentType,
          "Accept-Encoding": "identity",
        },
      })
      reader = response.body?.getReader()
      if (settled) {
        void reader?.cancel().catch(() => undefined)
        throw unavailable()
      }
      const encoding = response.headers.get("content-encoding")
      if (encoding && encoding !== "identity") throw unavailable()
      // MediaMTX 1.20 returns 400 (not 404) when this device has never had a
      // recording directory, or the retention cleaner removed the empty one.
      // Recognize only its exact ENOENT shape; other configuration/I/O errors
      // remain unavailable. The bounded diagnostic is never returned or logged.
      if (
        (response.status === 400 || response.status === 404) &&
        reader &&
        response.headers.get("content-type")?.split(";", 1)[0].trim() ===
          "application/json"
      ) {
        const chunks: Uint8Array[] = []
        let size = 0
        while (true) {
          const { done, value } = await reader.read()
          if (settled) throw unavailable()
          if (done) break
          size += value.byteLength
          if (size > 8192) throw unavailable()
          chunks.push(value)
        }
        const body = new Uint8Array(size)
        let offset = 0
        for (const chunk of chunks) {
          body.set(chunk, offset)
          offset += chunk.byteLength
        }
        try {
          const error = JSON.parse(
            new TextDecoder("utf-8", { fatal: true }).decode(body),
          )
          if (
            error?.status === "error" &&
            typeof error.error === "string" &&
            ((response.status === 404 &&
              error.error === "no recording segments found") ||
              (response.status === 400 &&
                error.error.startsWith("lstat ") &&
                error.error.endsWith(`/${path}: no such file or directory`)))
          )
            throw new RecordingNotFound()
        } catch (error) {
          if (error instanceof RecordingNotFound) throw error
        }
        throw unavailable()
      }
      const length = response.headers.get("content-length")
      if (
        response.status !== 200 ||
        !reader ||
        response.headers.get("content-type")?.split(";", 1)[0].trim() !==
          contentType ||
        (length !== null &&
          (!/^\d+$/.test(length) || Number(length) > maximumBytes))
      )
        throw unavailable()
      let bytes = 0
      return new ReadableStream<Uint8Array>(
        {
          start(stream) {
            output = stream
          },
          async pull(stream) {
            try {
              const { done, value } = await reader!.read()
              if (settled) return
              if (done) {
                stream.close()
                finish()
                return
              }
              bytes += value.byteLength
              if (bytes > maximumBytes) throw unavailable()
              stream.enqueue(value)
            } catch {
              if (!settled) {
                stream.error(unavailable())
                finish()
              }
            }
          },
          cancel() {
            finish()
          },
        },
        { highWaterMark: 0 },
      )
    } catch (error) {
      finish()
      if (error instanceof RecordingNotFound) throw error
      throw unavailable()
    }
  }
}

export function parseRecordingSpans(
  raw: unknown,
  windowStart: number,
  windowEnd: number,
): RecordingSpan[] {
  if (!Array.isArray(raw) || raw.length > 256) throw unavailable()
  const spans: RecordingSpan[] = []
  let previousEnd = -Infinity
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") throw unavailable()
    const start = timestamp(entry.start)
    const duration = entry.duration
    if (
      start === null ||
      typeof duration !== "number" ||
      !Number.isFinite(duration) ||
      duration <= 0 ||
      duration > 86400
    )
      throw unavailable()
    const end = start + duration * 1000
    if (start < previousEnd - 1) throw unavailable()
    previousEnd = end
    const clippedStart = Math.max(start, windowStart)
    const clippedEnd = Math.min(end, windowEnd)
    if (clippedEnd > clippedStart)
      spans.push({
        start: new Date(clippedStart).toISOString(),
        end: new Date(clippedEnd).toISOString(),
      })
    // Do not merge adjacent spans: codec changes can start a new MP4 track.
    // Ignore the upstream's absolute `url`, even if its origin looks familiar.
  }
  return spans
}

function timestamp(raw: unknown): number | null {
  if (
    typeof raw !== "string" ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(
      raw,
    )
  )
    return null
  const value = Date.parse(raw)
  return Number.isFinite(value) ? value : null
}
