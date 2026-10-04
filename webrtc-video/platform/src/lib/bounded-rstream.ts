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
// another request gets a fresh client and a fresh control-plane lookup.
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

export async function createPlatformRstreamClient(
  options: PlatformRstreamOptions,
  signal?: AbortSignal,
) {
  signal?.throwIfAborted()
  let project: TunnelsProject | undefined
  if (options.projectEndpoint) {
    const controlPlane = new RstreamClient({
      apiUrl: options.apiUrl,
      credentials: options.credentials,
      fetch: boundedFetch(signal),
    })
    project = await controlPlane.tunnels.projects.resolveByEndpoint(
      options.projectEndpoint,
    )
    if (options.projectId && options.projectId !== project.id)
      throw new Error(
        "rstream project ID and endpoint identify different projects",
      )
  }
  if (!options.engine && !project)
    throw new Error("An rstream engine or managed project endpoint is required")
  return new PlatformRstreamClient(options, project, signal)
}
