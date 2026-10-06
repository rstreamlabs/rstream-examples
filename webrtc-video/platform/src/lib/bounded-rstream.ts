import {
  RstreamClient,
  getTunnelsProjectEngine,
  type TunnelsProject,
} from "@rstreamlabs/rstream"
import {
  RstreamTunnelsClient,
  normalizeEngineAddress,
} from "@rstreamlabs/tunnels"

export type PlatformRstreamOptions = {
  apiUrl: string
  credentials: { clientId: string; clientSecret: string }
  engine?: string
  projectId?: string
  projectEndpoint?: string
}

// A request's AbortSignal is its lifetime/identity, not a cache key shared by
// users or devices. Concurrent credential branches reuse project resolution;
// another request gets a fresh client, optionally joining an in-flight lookup.
export function requestScopedClient<T>(
  create: (signal?: AbortSignal) => Promise<T>,
) {
  const requests = new WeakMap<AbortSignal, Promise<T>>()
  return async (signal?: AbortSignal): Promise<T> => {
    signal?.throwIfAborted()
    let client = signal ? requests.get(signal) : undefined
    if (!client) {
      client = create(signal)
      if (signal) requests.set(signal, client)
    }
    const resolved = await client
    signal?.throwIfAborted()
    return resolved
  }
}

// Share only work that is still in progress. Each caller owns its subscription;
// no caller can cancel another, and the last departure cancels upstream work.
// Results and failures are discarded before subscribers are notified.
export function coalesceInFlight<T>(load: (signal: AbortSignal) => Promise<T>) {
  type Flight = {
    controller: AbortController
    waiters: number
    result: Promise<T>
  }
  let current: Flight | undefined
  return async (signal?: AbortSignal): Promise<T> => {
    signal?.throwIfAborted()
    if (!current) {
      const controller = new AbortController()
      const flight: Flight = {
        controller,
        waiters: 0,
        result: Promise.resolve()
          .then(() => {
            controller.signal.throwIfAborted()
            return load(controller.signal)
          })
          .finally(() => {
            if (current === flight) current = undefined
          }),
      }
      current = flight
    }
    const flight = current
    flight.waiters++
    return new Promise<T>((resolve, reject) => {
      let finished = false
      const finish = (deliver: () => void) => {
        if (finished) return
        finished = true
        signal?.removeEventListener("abort", cancel)
        flight.waiters--
        if (flight.waiters === 0 && current === flight) {
          current = undefined
          flight.controller.abort()
        }
        deliver()
      }
      const cancel = () => finish(() => reject(signal!.reason))
      signal?.addEventListener("abort", cancel, { once: true })
      flight.result.then(
        (value) =>
          finish(() => {
            if (signal?.aborted) reject(signal.reason)
            else resolve(value)
          }),
        (error: unknown) => finish(() => reject(error)),
      )
      if (signal?.aborted) cancel()
    })
  }
}

// The SDK accepts fetch injection on its control-plane client. Buffer only a
// bounded body while the abort deadline is active, including stalled bodies.
export function boundedFetch(
  parent?: AbortSignal,
  maximumBytes = 1024 * 1024,
  timeoutMs = 5000,
): typeof fetch {
  return async (input, init) => {
    const request = new Request(input, init)
    const controller = new AbortController()
    const parents = [request.signal, ...(parent ? [parent] : [])]
    const listeners = parents.map((signal) => {
      const abort = () => controller.abort(signal.reason)
      signal.addEventListener("abort", abort, { once: true })
      if (signal.aborted) abort()
      return () => signal.removeEventListener("abort", abort)
    })
    const timeout = setTimeout(
      () => controller.abort(new Error("rstream request timed out")),
      timeoutMs,
    )
    try {
      const response = await fetch(request, {
        redirect: "error",
        signal: controller.signal,
      })
      if (!response.ok) {
        await response.body?.cancel()
        throw new Error(`rstream request failed (${response.status})`)
      }
      if (!response.body) return response
      const reader = response.body.getReader()
      const chunks: Uint8Array[] = []
      let size = 0
      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          size += value.length
          if (size > maximumBytes) {
            await reader.cancel()
            throw new Error("rstream response exceeded its limit")
          }
          chunks.push(value)
        }
      } finally {
        reader.releaseLock()
      }
      const headers = new Headers(response.headers)
      headers.delete("content-encoding")
      headers.delete("content-length")
      return new Response(Buffer.concat(chunks), {
        status: response.status,
        statusText: response.statusText,
        headers,
      })
    } finally {
      clearTimeout(timeout)
      for (const remove of listeners) remove()
    }
  }
}

class PlatformRstreamClient extends RstreamTunnelsClient {
  private readonly requestFetch: typeof fetch
  private readonly target?: TunnelsProject
  constructor(
    options: PlatformRstreamOptions,
    project?: TunnelsProject,
    signal?: AbortSignal,
  ) {
    super({
      apiUrl: options.apiUrl,
      credentials: options.credentials,
      engine:
        options.engine ??
        (project ? getTunnelsProjectEngine(project) : undefined),
      projectId: options.projectId ?? project?.id,
    })
    this.target = project
    this.requestFetch = boundedFetch(signal)
  }
  override async getTURNTarget() {
    const project = this.target
    if (!project?.turnRealm)
      throw new Error("A managed project with a TURN realm is required.")
    return {
      turnDomain: project.turnDomain ?? project.domain,
      turnRealm: project.turnRealm,
      turnPort: project.turnPort,
      turnsPort: project.turnsPort,
    }
  }
  override async request<T>(path: string, options?: RequestInit): Promise<T> {
    if (!path.startsWith("/") || path.startsWith("//"))
      throw new Error("Invalid engine API path")
    const engine = normalizeEngineAddress(await this.getEngine())
    const headers = new Headers(options?.headers)
    const token = await this.getToken(engine)
    if (token) headers.set("Authorization", `Bearer ${token}`)
    const response = await this.requestFetch(`https://${engine}/api${path}`, {
      ...options,
      headers,
    })
    if (!response.ok)
      throw new Error(`rstream engine request failed (${response.status})`)
    return (await response.json()) as T
  }
}

export function platformRstreamClientFactory() {
  let resolution:
    | {
        options: PlatformRstreamOptions
        resolve: (signal?: AbortSignal) => Promise<TunnelsProject>
      }
    | undefined
  return async (options: PlatformRstreamOptions, signal?: AbortSignal) => {
    signal?.throwIfAborted()
    let project: TunnelsProject | undefined
    if (options.projectEndpoint) {
      const previous = resolution?.options
      if (
        !previous ||
        previous.apiUrl !== options.apiUrl ||
        previous.credentials.clientId !== options.credentials.clientId ||
        previous.credentials.clientSecret !==
          options.credentials.clientSecret ||
        previous.projectEndpoint !== options.projectEndpoint
      ) {
        // Only one configuration is retained. In-flight requests using an older
        // configuration keep their own bounded operation and cannot replace it.
        const snapshot = {
          ...options,
          credentials: { ...options.credentials },
        }
        resolution = {
          options: snapshot,
          resolve: coalesceInFlight(async (sharedSignal) => {
            const controlPlane = new RstreamClient({
              apiUrl: snapshot.apiUrl,
              credentials: snapshot.credentials,
              fetch: boundedFetch(sharedSignal),
            })
            return controlPlane.tunnels.projects.resolveByEndpoint(
              snapshot.projectEndpoint!,
            )
          }),
        }
      }
      project = await resolution!.resolve(signal)
      signal?.throwIfAborted()
      if (options.projectId && options.projectId !== project.id)
        throw new Error(
          "rstream project ID and endpoint identify different projects",
        )
    }
    if (!options.engine && !project)
      throw new Error(
        "An rstream engine or managed project endpoint is required",
      )
    return new PlatformRstreamClient(options, project, signal)
  }
}
