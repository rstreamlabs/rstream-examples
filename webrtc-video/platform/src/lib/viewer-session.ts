export type ViewerPhase =
  "connecting" | "reconnecting" | "blocked" | "error" | "playing"

export type ViewerPeer = {
  connectionState: string
  onconnectionstatechange: ((event: Event) => unknown) | null
}

export type ViewerSessionClient = {
  close: () => Promise<unknown>
  peer: ViewerPeer
  restart: () => Promise<void>
  start: () => Promise<void>
}

export type ViewerClientCallbacks<Resolution, Track> = {
  onError: (cause: unknown) => void
  onTrack: (track: Track) => void
  refresh: (signal: AbortSignal) => Promise<Resolution>
}

type ViewerSessionControllerOptions<Resolution, Track> = {
  backend: (resolution: Resolution) => string
  createClient: (
    resolution: Resolution,
    callbacks: ViewerClientCallbacks<Resolution, Track>,
  ) => ViewerSessionClient
  delayForAttempt?: (attempt: number, minimumDelayMs: number) => number
  disconnectGraceMs?: number
  excludeBackendAfterFailure?: (backend: string, cause: unknown) => boolean
  maximumICERestarts?: number
  maximumSessionReconnects?: number
  onFailure: (cause: unknown) => void
  onPhase: (phase: ViewerPhase) => void
  onSessionReset?: () => void
  onTrack: (track: Track, resolution: Resolution) => void
  resolve: (
    signal: AbortSignal,
    excludedBackend: string | null,
  ) => Promise<Resolution>
  restartOutcomeTimeoutMs?: number
  stableSessionMs?: number
}

const reconnectBaseDelayMs = 1000
const reconnectJitterMs = 250
const reconnectMaxDelayMs = 15000
const maximumTimerDelayMs = 2_147_483_647
const defaultDisconnectGraceMs = 1000
const defaultMaximumICERestarts = 2
const defaultMaximumSessionReconnects = 5
const defaultRestartOutcomeTimeoutMs = 15000

export const viewerStableSessionMs = 30000
export const viewerRequestTimeoutMs = 15000

export class ViewerDistributorChangedError extends Error {
  constructor() {
    super("Video distributor changed during the viewer session")
    this.name = "ViewerDistributorChangedError"
  }
}

export class ReconnectBudget {
  private attempts = 0
  private readonly maximumAttempts: number

  constructor(maximumAttempts: number) {
    if (!Number.isSafeInteger(maximumAttempts) || maximumAttempts < 1) {
      throw new Error("Reconnect attempt limit must be a positive integer")
    }
    this.maximumAttempts = maximumAttempts
  }

  next() {
    if (this.attempts >= this.maximumAttempts) {
      return null
    }
    this.attempts += 1
    return this.attempts
  }

  reset() {
    this.attempts = 0
  }
}

export class ViewerSessionController<Resolution, Track = unknown> {
  private readonly backend: (resolution: Resolution) => string
  private readonly createClient: ViewerSessionControllerOptions<
    Resolution,
    Track
  >["createClient"]
  private readonly delayForAttempt: (attempt: number, minimum: number) => number
  private readonly disconnectGraceMs: number
  private readonly excludeBackendAfterFailure: (
    backend: string,
    cause: unknown,
  ) => boolean
  private readonly onFailure: (cause: unknown) => void
  private readonly onPhase: (phase: ViewerPhase) => void
  private readonly onSessionReset: () => void
  private readonly onTrack: (track: Track, resolution: Resolution) => void
  private readonly resolve: ViewerSessionControllerOptions<
    Resolution,
    Track
  >["resolve"]
  private readonly restartOutcomeTimeoutMs: number
  private readonly stableSessionMs: number
  private readonly reconnectBudget: ReconnectBudget
  private readonly restartBudget: ReconnectBudget
  private client: ViewerSessionClient | null = null
  private reconnectDelayResolve: (() => void) | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private resolverAbort: AbortController | null = null
  private restartInFlight = false
  private restartOutcomeTimer: ReturnType<typeof setTimeout> | null = null
  private restartTimer: ReturnType<typeof setTimeout> | null = null
  private stableTimer: ReturnType<typeof setTimeout> | null = null
  private reconnecting = false
  private currentBackend: string | null = null
  private excludedBackend: string | null = null
  private started = false
  private stopped = false

  constructor(options: ViewerSessionControllerOptions<Resolution, Track>) {
    this.backend = options.backend
    this.createClient = options.createClient
    this.delayForAttempt =
      options.delayForAttempt ??
      ((attempt, minimumDelayMs) =>
        reconnectDelay(attempt, Math.random, minimumDelayMs))
    this.disconnectGraceMs = positiveDuration(
      options.disconnectGraceMs ?? defaultDisconnectGraceMs,
      "Viewer disconnect grace period",
      true,
    )
    this.excludeBackendAfterFailure =
      options.excludeBackendAfterFailure ?? (() => false)
    this.stableSessionMs = positiveDuration(
      options.stableSessionMs ?? viewerStableSessionMs,
      "Viewer stable-session period",
      true,
    )
    this.reconnectBudget = new ReconnectBudget(
      options.maximumSessionReconnects ?? defaultMaximumSessionReconnects,
    )
    this.restartBudget = new ReconnectBudget(
      options.maximumICERestarts ?? defaultMaximumICERestarts,
    )
    this.restartOutcomeTimeoutMs = positiveDuration(
      options.restartOutcomeTimeoutMs ?? defaultRestartOutcomeTimeoutMs,
      "Viewer ICE restart outcome timeout",
      false,
    )
    this.onFailure = options.onFailure
    this.onPhase = options.onPhase
    this.onSessionReset = options.onSessionReset ?? (() => {})
    this.onTrack = options.onTrack
    this.resolve = options.resolve
  }

  async start() {
    if (this.started) {
      throw new Error("Viewer session controller was already started")
    }
    if (this.stopped) {
      throw new Error("Viewer session controller is stopped")
    }
    this.started = true
    await this.connect()
  }

  async stop() {
    if (this.stopped) {
      return
    }
    this.stopped = true
    this.resolverAbort?.abort(new Error("Viewer session stopped"))
    this.resolverAbort = null
    this.clearReconnectTimer()
    const client = this.detachClient()
    if (client) {
      await client.close().catch(() => {})
    }
  }

  excludeCurrentBackend(cause: unknown) {
    if (this.stopped || this.reconnecting || !this.currentBackend) {
      return false
    }
    const backend = this.currentBackend
    this.excludeBackendAfterFailure(backend, cause)
    this.excludedBackend = backend
    return this.scheduleSessionReconnect(cause)
  }

  private async connect(phase: "connecting" | "reconnecting" = "connecting") {
    if (this.stopped) {
      return
    }
    this.onPhase(phase)
    const resolverAbort = new AbortController()
    this.resolverAbort = resolverAbort
    let resolution: Resolution
    try {
      resolution = await this.resolve(
        resolverAbort.signal,
        this.excludedBackend,
      )
    } catch (cause) {
      if (!this.stopped && this.resolverAbort === resolverAbort) {
        this.scheduleSessionReconnect(cause)
      }
      return
    } finally {
      if (this.resolverAbort === resolverAbort) {
        this.resolverAbort = null
      }
    }
    if (this.stopped) {
      return
    }
    const backend = this.backend(resolution)
    if (backend === this.excludedBackend) {
      this.scheduleSessionReconnect(
        new Error(`Excluded video distributor was resolved: ${backend}`),
      )
      return
    }
    let client: ViewerSessionClient | null = null
    const callbacks: ViewerClientCallbacks<Resolution, Track> = {
      onError: (cause) => {
        if (client && this.client === client) {
          this.scheduleRecovery(cause)
        }
      },
      onTrack: (track) => {
        if (client && this.client === client && !this.stopped) {
          this.onTrack(track, resolution)
        }
      },
      refresh: (signal) => this.refresh(client, resolution, signal),
    }
    try {
      client = this.createClient(resolution, callbacks)
    } catch (cause) {
      this.scheduleSessionReconnect(cause)
      return
    }
    if (this.stopped) {
      await client.close().catch(() => {})
      return
    }
    this.client = client
    this.currentBackend = backend
    client.peer.onconnectionstatechange = () =>
      this.handleConnectionState(client)
    try {
      await client.start()
      this.handleConnectionState(client)
    } catch (cause) {
      if (this.client === client && !this.stopped) {
        this.scheduleRecovery(cause)
      }
    }
  }

  private async refresh(
    client: ViewerSessionClient | null,
    current: Resolution,
    signal: AbortSignal,
  ) {
    const refreshed = await this.resolve(signal, this.excludedBackend)
    if (this.stopped || !client || this.client !== client) {
      throw new Error("Viewer session changed during credential refresh")
    }
    if (this.backend(refreshed) !== this.backend(current)) {
      throw new ViewerDistributorChangedError()
    }
    return refreshed
  }

  private handleConnectionState(client: ViewerSessionClient) {
    if (this.stopped || this.client !== client) {
      return
    }
    switch (client.peer.connectionState) {
      case "connected":
        this.clearRestartTimer()
        this.clearRestartOutcomeTimer()
        if (!this.stableTimer) {
          this.stableTimer = setTimeout(() => {
            this.stableTimer = null
            this.restartBudget.reset()
            this.reconnectBudget.reset()
          }, this.stableSessionMs)
        }
        break
      case "disconnected":
        this.clearStableTimer()
        if (!this.restartInFlight) {
          this.clearRestartOutcomeTimer()
        }
        this.scheduleICERestart(client, this.disconnectGraceMs)
        break
      case "failed":
        this.clearStableTimer()
        if (!this.restartInFlight) {
          this.clearRestartOutcomeTimer()
        }
        this.scheduleICERestart(client, 0)
        break
      default:
        break
    }
  }

  private scheduleICERestart(client: ViewerSessionClient, delay: number) {
    if (
      this.stopped ||
      this.client !== client ||
      this.restartTimer ||
      this.restartInFlight
    ) {
      return
    }
    this.onPhase("reconnecting")
    this.restartTimer = setTimeout(() => {
      this.restartTimer = null
      if (this.stopped || this.client !== client) {
        return
      }
      if (this.restartBudget.next() === null) {
        this.scheduleRecovery(new Error("WHEP ICE restart limit reached"))
        return
      }
      this.restartInFlight = true
      this.restartOutcomeTimer = setTimeout(() => {
        this.restartOutcomeTimer = null
        if (!this.stopped && this.client === client) {
          this.restartInFlight = false
          this.scheduleRecovery(
            new Error("WHEP ICE restart did not restore connectivity"),
          )
        }
      }, this.restartOutcomeTimeoutMs)
      void client.restart().then(
        () => {
          if (this.stopped || this.client !== client) {
            return
          }
          this.restartInFlight = false
          this.handleConnectionState(client)
        },
        (cause) => {
          if (this.stopped || this.client !== client) {
            return
          }
          this.restartInFlight = false
          this.clearRestartOutcomeTimer()
          if (requiresFullViewerReconnect(cause)) {
            this.scheduleRecovery(cause)
            return
          }
          this.scheduleICERestart(
            client,
            Math.max(500, retryAfterMilliseconds(cause)),
          )
        },
      )
    }, delay)
  }

  private scheduleSessionReconnect(cause: unknown) {
    if (this.stopped || this.reconnecting) {
      return false
    }
    const attempt = this.reconnectBudget.next()
    if (attempt === null) {
      this.fail(new Error("Viewer reconnect limit reached.", { cause }))
      return false
    }
    this.reconnecting = true
    this.onPhase("reconnecting")
    this.resolverAbort?.abort(new Error("Viewer session is reconnecting"))
    this.resolverAbort = null
    const client = this.detachClient()
    this.onSessionReset()
    let reconnectDelayMs: number
    try {
      reconnectDelayMs = this.delayForAttempt(
        attempt,
        retryAfterMilliseconds(cause),
      )
      validateDuration(reconnectDelayMs, "Viewer reconnect delay", true)
    } catch (error) {
      this.reconnecting = false
      this.fail(error)
      return
    }
    const wait = this.waitForReconnectDelay(reconnectDelayMs)
    const close = client ? client.close().catch(() => {}) : Promise.resolve()
    void Promise.all([wait, close]).then(() => {
      this.reconnecting = false
      if (!this.stopped) {
        void this.connect("reconnecting").catch((error) => this.fail(error))
      }
    })
    return true
  }

  private scheduleRecovery(cause: unknown) {
    const backend = this.currentBackend
    if (backend && this.excludeBackendAfterFailure(backend, cause)) {
      this.excludedBackend = backend
    }
    return this.scheduleSessionReconnect(cause)
  }

  private detachClient() {
    const client = this.client
    this.client = null
    this.currentBackend = null
    if (client) {
      client.peer.onconnectionstatechange = null
    }
    this.restartInFlight = false
    this.clearRestartTimer()
    this.clearRestartOutcomeTimer()
    this.clearStableTimer()
    return client
  }

  private waitForReconnectDelay(delay: number) {
    return new Promise<void>((resolve) => {
      this.reconnectDelayResolve = resolve
      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = null
        this.reconnectDelayResolve = null
        resolve()
      }, delay)
    })
  }

  private clearReconnectTimer() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    const resolve = this.reconnectDelayResolve
    this.reconnectDelayResolve = null
    resolve?.()
  }

  private clearRestartTimer() {
    if (this.restartTimer) {
      clearTimeout(this.restartTimer)
      this.restartTimer = null
    }
  }

  private clearRestartOutcomeTimer() {
    if (this.restartOutcomeTimer) {
      clearTimeout(this.restartOutcomeTimer)
      this.restartOutcomeTimer = null
    }
  }

  private clearStableTimer() {
    if (this.stableTimer) {
      clearTimeout(this.stableTimer)
      this.stableTimer = null
    }
  }

  private fail(cause: unknown) {
    if (this.stopped) {
      return
    }
    this.stopped = true
    this.clearReconnectTimer()
    const client = this.detachClient()
    this.onSessionReset()
    if (client) {
      void client.close().catch(() => {})
    }
    this.onFailure(cause)
  }
}

export function reconnectDelay(
  attempt: number,
  random: () => number = Math.random,
  minimumDelayMs: number = 0,
) {
  if (!Number.isSafeInteger(attempt) || attempt < 1) {
    throw new Error("Reconnect attempt must be a positive integer")
  }
  if (!Number.isSafeInteger(minimumDelayMs) || minimumDelayMs < 0) {
    throw new Error("Reconnect minimum delay must be a non-negative integer")
  }
  const exponentialDelay = reconnectBaseDelayMs * 2 ** (attempt - 1)
  const boundedDelay = Math.min(exponentialDelay, reconnectMaxDelayMs)
  const sample = random()
  if (!Number.isFinite(sample) || sample < 0 || sample >= 1) {
    throw new Error("Reconnect jitter source must return a number in [0, 1)")
  }
  const jitter = Math.floor(sample * reconnectJitterMs)
  return Math.max(boundedDelay + jitter, minimumDelayMs)
}

export function viewerRequestSignal(
  parent: AbortSignal,
  timeoutMs: number = viewerRequestTimeoutMs,
) {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1) {
    throw new Error("Viewer request timeout must be a positive integer")
  }
  return AbortSignal.any([parent, AbortSignal.timeout(timeoutMs)])
}

export function requiresFullViewerReconnect(cause: unknown) {
  return cause instanceof ViewerDistributorChangedError
}

function retryAfterMilliseconds(cause: unknown) {
  if (
    typeof cause !== "object" ||
    cause === null ||
    !("retryAfterMilliseconds" in cause)
  ) {
    return 0
  }
  const delay = cause.retryAfterMilliseconds
  return Number.isSafeInteger(delay) && Number(delay) >= 0
    ? Math.min(Number(delay), maximumTimerDelayMs)
    : 0
}

function positiveDuration(value: number, name: string, allowZero: boolean) {
  validateDuration(value, name, allowZero)
  return value
}

function validateDuration(value: number, name: string, allowZero: boolean) {
  if (
    !Number.isSafeInteger(value) ||
    value < 0 ||
    (!allowZero && value === 0)
  ) {
    throw new Error(
      `${name} must be ${allowZero ? "a non-negative" : "a positive"} integer`,
    )
  }
}
