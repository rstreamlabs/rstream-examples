// Read-only, process-local observation of MediaMTX path counters. Authorization
// belongs at the route boundary and must run before every cached read.
export type MediaMTXMetrics = {
  state: "ready" | "idle"
  readers: number
  inboundBitsPerSecond: number | null
  outboundBitsPerSecond: number | null
  sampledAt: string
  intervalMs: number | null
}

type Counters = {
  state: "ready" | "idle"
  readers: number
  inbound: bigint
  outbound: bigint
}
type Observation = { counters: Counters; at: number }
type Entry = {
  nextReadAt: number
  observation?: Observation
  result?: MediaMTXMetrics
}
type Job = {
  controller: AbortController
  promise: Promise<MediaMTXMetrics>
  waiters: number
}

const cacheMs = 2000
const deadlineMs = 3000
const maximumIntervalMs = 15000
const maximumBodyBytes = 32 * 1024
const maximumEntries = 128
const maximumInFlight = 8
const maximumWaiters = 64
const unavailable = () => new Error("MediaMTX metrics are unavailable")

export function validateMetricsURL(raw: string) {
  const url = new URL(raw)
  if (
    raw.length > 2048 ||
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !url.pathname.endsWith("/metrics")
  )
    throw new Error(
      "MEDIAMTX_METRICS_URL must be an HTTP(S) metrics endpoint without credentials, query or fragment",
    )
  return url
}

export class MediaMTXMetricsReader {
  private readonly endpoint: URL
  private readonly fetcher: typeof fetch
  private readonly clock: () => number
  private readonly entries = new Map<string, Entry>()
  private readonly pending = new Map<string, Job>()

  constructor(options: {
    endpoint: string
    fetch?: typeof fetch
    clock?: () => number
  }) {
    this.endpoint = validateMetricsURL(options.endpoint)
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis)
    this.clock = options.clock ?? (() => performance.now())
  }

  async read(path: string, signal: AbortSignal): Promise<MediaMTXMetrics> {
    signal.throwIfAborted()
    // Only the platform's fixed UUID namespace is accepted, never an arbitrary
    // path/query supplied by a browser or discovered tunnel metadata.
    if (
      !/^devices\/[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
        path,
      )
    )
      throw unavailable()
    const entry = this.entries.get(path)
    if (entry && this.clock() < entry.nextReadAt) {
      if (!entry.result) throw unavailable()
      return { ...entry.result }
    }
    let job = this.pending.get(path)
    if (!job) {
      if (this.pending.size >= maximumInFlight) throw unavailable()
      const controller = new AbortController()
      const promise = this.scrape(path, controller.signal, entry).finally(
        () => {
          this.pending.delete(path)
        },
      )
      job = { controller, promise, waiters: 0 }
      this.pending.set(path, job)
    }
    if (job.controller.signal.aborted || job.waiters >= maximumWaiters)
      throw unavailable()
    return this.subscribe(job, signal)
  }

  private subscribe(job: Job, signal: AbortSignal): Promise<MediaMTXMetrics> {
    job.waiters++
    return new Promise((resolve, reject) => {
      let settled = false
      const finish = (error?: unknown, result?: MediaMTXMetrics) => {
        if (settled) return
        settled = true
        signal.removeEventListener("abort", onAbort)
        job.waiters--
        if (job.waiters === 0) job.controller.abort()
        if (error) reject(error)
        else resolve({ ...result! })
      }
      const onAbort = () => finish(signal.reason ?? unavailable())
      signal.addEventListener("abort", onAbort, { once: true })
      if (signal.aborted) onAbort()
      // Every subscriber observes rejection, including after cancellation.
      void job.promise.then(
        (value) => finish(undefined, value),
        (error) => finish(error),
      )
    })
  }

  private remember(path: string, entry: Entry) {
    this.entries.delete(path)
    if (this.entries.size >= maximumEntries)
      this.entries.delete(this.entries.keys().next().value!)
    this.entries.set(path, entry)
  }

  private async scrape(
    path: string,
    cancellation: AbortSignal,
    previous?: Entry,
  ) {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), deadlineMs)
    const signal = AbortSignal.any([cancellation, controller.signal])
    try {
      const url = new URL(this.endpoint)
      url.searchParams.set("type", "paths")
      url.searchParams.set("path", path)
      const response = await this.fetcher(url, {
        signal,
        cache: "no-store",
        redirect: "error",
        credentials: "omit",
        headers: {
          Accept: "text/plain, application/openmetrics-text",
          "Accept-Encoding": "identity",
        },
      })
      const contentType = response.headers
        .get("content-type")
        ?.split(";", 1)[0]
        ?.trim()
      if (
        !response.ok ||
        (contentType &&
          !["text/plain", "application/openmetrics-text"].includes(contentType))
      ) {
        await response.body?.cancel()
        throw unavailable()
      }
      const body = await readBody(response, signal)
      // MediaMTX returns an empty 200 without Content-Type when this path has
      // never been instantiated. Nonempty responses still require a text type.
      if (!contentType && body !== "") throw unavailable()
      const counters = parseMediaMTXMetrics(body, path)
      signal.throwIfAborted()
      const at = this.clock()
      const old = previous?.observation
      const elapsed = old ? at - old.at : 0
      const measurable =
        old &&
        elapsed >= cacheMs &&
        elapsed <= maximumIntervalMs &&
        old.counters.state === "ready" &&
        counters.state === "ready" &&
        counters.inbound >= old.counters.inbound &&
        counters.outbound >= old.counters.outbound
      const result: MediaMTXMetrics = {
        state: counters.state,
        readers: counters.readers,
        inboundBitsPerSecond: measurable
          ? Math.round(
              (Number(counters.inbound - old.counters.inbound) * 8000) /
                elapsed,
            )
          : null,
        outboundBitsPerSecond: measurable
          ? Math.round(
              (Number(counters.outbound - old.counters.outbound) * 8000) /
                elapsed,
            )
          : null,
        sampledAt: new Date().toISOString(),
        intervalMs: measurable ? Math.round(elapsed) : null,
      }
      this.remember(path, {
        nextReadAt: at + cacheMs,
        observation: { counters, at },
        result,
      })
      return result
    } catch {
      // Failed scrapes break the rate baseline and are briefly cached too.
      // Client cancellation must neither poison another viewer nor fill cache.
      if (!cancellation.aborted)
        this.remember(path, { nextReadAt: this.clock() + cacheMs })
      throw unavailable()
    } finally {
      clearTimeout(timer)
    }
  }
}

async function readBody(response: Response, signal: AbortSignal) {
  const length = response.headers.get("content-length")
  if (length && (!/^\d+$/.test(length) || Number(length) > maximumBodyBytes)) {
    await response.body?.cancel()
    throw unavailable()
  }
  const reader = response.body?.getReader()
  if (!reader) return ""
  const onAbort = () => {
    void reader.cancel().catch(() => {})
  }
  signal.addEventListener("abort", onAbort, { once: true })
  if (signal.aborted) onAbort()
  let size = 0
  const decoder = new TextDecoder("utf-8", { fatal: true })
  let text = ""
  try {
    while (true) {
      const { done, value } = await reader.read()
      signal.throwIfAborted()
      if (done) break
      size += value.byteLength
      if (size > maximumBodyBytes) throw unavailable()
      text += decoder.decode(value, { stream: true })
    }
    return text + decoder.decode()
  } finally {
    signal.removeEventListener("abort", onAbort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

// Parse only the four documented path families. Other paths and unrelated
// families never enter the returned object, even if an upstream ignores filters.
export function parseMediaMTXMetrics(text: string, path: string): Counters {
  const scalar = new Map<string, bigint>()
  const readers = new Map<string, bigint>()
  let state: "ready" | "idle" | undefined
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim() || line.startsWith("#")) continue
    if (
      !/^(paths|paths_readers|paths_inbound_bytes|paths_outbound_bytes)(?:\{|\s)/.test(
        line,
      )
    ) {
      if (
        !/^[a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^\r\n]*\})?\s+(?:[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?|NaN|[+-]?Inf)(?:\s+\d+)?\s*$/.test(
          line,
        )
      )
        throw unavailable()
      continue
    }
    const match =
      /^(paths|paths_readers|paths_inbound_bytes|paths_outbound_bytes)\{([^{}]*)\}\s+(\d{1,20})\s*$/.exec(
        line,
      )
    if (!match) throw unavailable()
    const labels = new Map<string, string>()
    let offset = 0
    const label =
      /([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\\r\n]|\\[\\"n])*)"(?:,|$)/gy
    while (offset < match[2].length) {
      label.lastIndex = offset
      const item = label.exec(match[2])
      if (!item || labels.has(item[1])) throw unavailable()
      labels.set(
        item[1],
        item[2].replace(/\\([\\"n])/g, (_, escape: string) =>
          escape === "n" ? "\n" : escape,
        ),
      )
      offset = label.lastIndex
    }
    if (labels.get("name") !== path) continue
    const rawState = labels.get("state")
    if (rawState !== "ready" && rawState !== "notReady") throw unavailable()
    const nextState = rawState === "ready" ? "ready" : "idle"
    if (state && state !== nextState) throw unavailable()
    state = nextState
    const value = BigInt(match[3])
    if (value > 18446744073709551615n) throw unavailable()
    if (match[1] === "paths_readers") {
      const type = labels.get("readerType") ?? ""
      if (readers.has(type)) throw unavailable()
      readers.set(type, value)
    } else {
      if (scalar.has(match[1])) throw unavailable()
      scalar.set(match[1], value)
    }
  }
  if (!state) return { state: "idle", readers: 0, inbound: 0n, outbound: 0n }
  if (
    scalar.get("paths") !== 1n ||
    !scalar.has("paths_inbound_bytes") ||
    !scalar.has("paths_outbound_bytes") ||
    readers.size === 0
  )
    throw unavailable()
  const count = [...readers.values()].reduce((sum, value) => sum + value, 0n)
  if (count > BigInt(Number.MAX_SAFE_INTEGER)) throw unavailable()
  return {
    state,
    readers: Number(count),
    inbound: scalar.get("paths_inbound_bytes")!,
    outbound: scalar.get("paths_outbound_bytes")!,
  }
}
