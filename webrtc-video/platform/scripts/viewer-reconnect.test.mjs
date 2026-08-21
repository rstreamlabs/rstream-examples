import assert from "node:assert/strict"
import { createServer } from "node:http"
import test from "node:test"

import {
  ReconnectBudget,
  reconnectDelay,
  requiresFullViewerReconnect,
  ViewerDistributorChangedError,
  ViewerSessionController,
  viewerRequestSignal,
} from "../src/lib/viewer-session.ts"
import { WHEPClient } from "../../shared/whep-client.ts"

test("viewer reconnect budget is bounded and resets after stability", () => {
  const budget = new ReconnectBudget(3)
  assert.deepEqual([budget.next(), budget.next(), budget.next()], [1, 2, 3])
  assert.equal(budget.next(), null)
  assert.equal(budget.next(), null)
  budget.reset()
  assert.equal(budget.next(), 1)
})

test("viewer reconnect delay is exponential, bounded, and jittered", () => {
  assert.equal(
    reconnectDelay(1, () => 0),
    1000,
  )
  assert.equal(
    reconnectDelay(2, () => 0.5),
    2125,
  )
  assert.equal(
    reconnectDelay(5, () => 0),
    15000,
  )
  assert.equal(
    reconnectDelay(20, () => 0.999),
    15249,
  )
  assert.equal(
    reconnectDelay(2, () => 0, 9000),
    9000,
  )
})

test("viewer reconnect policy rejects invalid attempts and budgets", () => {
  for (const maximum of [0, -1, 1.5, Number.NaN]) {
    assert.throws(() => new ReconnectBudget(maximum), /positive integer/)
  }
  for (const attempt of [0, -1, 1.5, Number.NaN]) {
    assert.throws(() => reconnectDelay(attempt), /positive integer/)
  }
  for (const minimum of [-1, 1.5, Number.NaN]) {
    assert.throws(
      () => reconnectDelay(1, () => 0, minimum),
      /non-negative integer/,
    )
  }
  for (const random of [() => -0.1, () => 1, () => Number.NaN]) {
    assert.throws(
      () => reconnectDelay(1, random),
      /jitter source must return a number in \[0, 1\)/,
    )
  }
  assert.throws(
    () => viewerRequestSignal(new AbortController().signal, 0),
    /positive integer/,
  )
})

test("viewer API requests honor parent cancellation and a deadline", async () => {
  const parent = new AbortController()
  const parentSignal = viewerRequestSignal(parent.signal, 1000)
  parent.abort(new Error("viewer stopped"))
  assert.equal(parentSignal.aborted, true)
  assert.match(String(parentSignal.reason), /viewer stopped/)
  const deadlineSignal = viewerRequestSignal(new AbortController().signal, 20)
  await new Promise((resolve) =>
    deadlineSignal.addEventListener("abort", resolve, { once: true }),
  )
  assert.equal(deadlineSignal.aborted, true)
  assert.equal(deadlineSignal.reason?.name, "TimeoutError")
})

test("a distributor change bypasses repeated ICE restarts", () => {
  assert.equal(
    requiresFullViewerReconnect(new ViewerDistributorChangedError()),
    true,
  )
  assert.equal(requiresFullViewerReconnect(new Error("network failure")), false)
})

test("a distributor change closes the old session before resolving a fresh backend", async () => {
  const events = []
  const phases = []
  const resets = []
  const failures = []
  const resolutions = [
    { authorization: "media-token", backend: "mediamtx" },
    { authorization: "direct-refresh-token", backend: "direct" },
    { authorization: "direct-session-token", backend: "direct" },
  ]
  const clients = []
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: (resolution, callbacks) => {
      const peer = {
        connectionState: "connected",
        onconnectionstatechange: null,
      }
      const client = {
        callbacks,
        close: async () => {
          events.push(`close-start:${resolution.backend}`)
          await new Promise((resolve) => setTimeout(resolve, 5))
          events.push(`close-end:${resolution.backend}`)
        },
        peer,
        restart: async () => {
          events.push(`restart:${resolution.backend}`)
          await callbacks.refresh(new AbortController().signal)
        },
        start: async () => {
          events.push(`start:${resolution.backend}:${resolution.authorization}`)
        },
      }
      clients.push(client)
      events.push(`create:${resolution.backend}`)
      return client
    },
    delayForAttempt: () => 0,
    disconnectGraceMs: 0,
    onFailure: (cause) => failures.push(cause),
    onPhase: (phase) => phases.push(phase),
    onSessionReset: () => resets.push("reset"),
    onTrack: () => {},
    resolve: async () => {
      const resolution = resolutions.shift()
      assert.ok(resolution, "unexpected viewer resolution")
      events.push(`resolve:${resolution.backend}:${resolution.authorization}`)
      return resolution
    },
    stableSessionMs: 60000,
  })
  await controller.start()
  clients[0].peer.connectionState = "failed"
  clients[0].peer.onconnectionstatechange()
  await eventually(() => clients.length === 2)
  assert.equal(failures.length, 0)
  assert.equal(resets.length, 1)
  assert.equal(phases[0], "connecting")
  assert.equal(
    phases.slice(1).every((phase) => phase === "reconnecting"),
    true,
  )
  assert.equal(
    events.indexOf("close-end:mediamtx") <
      events.indexOf("resolve:direct:direct-session-token"),
    true,
    "the next backend was resolved before the previous session closed",
  )
  assert.equal(
    events.includes("start:direct:direct-session-token"),
    true,
    "the refreshed credential must not leak into the fresh session",
  )
  await controller.stop()
  assert.equal(events.at(-1), "close-end:direct")
})

test("a live MediaMTX WHEP resource is deleted before direct fallback starts", async (t) => {
  const requests = []
  let mediaMTXDeleted = false
  const server = createServer(async (request, response) => {
    const body = await requestBody(request)
    requests.push({
      authorization: request.headers.authorization ?? "",
      body,
      method: request.method,
      url: request.url,
    })
    if (request.method === "POST" && request.url === "/mediamtx?edge=old") {
      response.writeHead(201, {
        "Content-Type": "application/sdp",
        ETag: "*",
        Location: "/mediamtx/session?edge=old",
      })
      response.end(whepAnswer)
      return
    }
    if (
      request.method === "DELETE" &&
      request.url === "/mediamtx/session?edge=old"
    ) {
      await new Promise((resolve) => setTimeout(resolve, 30))
      mediaMTXDeleted = true
      response.writeHead(204).end()
      return
    }
    if (request.method === "POST" && request.url === "/direct?edge=fresh") {
      assert.equal(mediaMTXDeleted, true)
      response.writeHead(201, {
        "Content-Type": "application/sdp",
        ETag: '"direct-generation"',
        Location: "/direct/session?edge=fresh",
      })
      response.end(whepAnswer)
      return
    }
    if (
      request.method === "DELETE" &&
      request.url === "/direct/session?edge=fresh"
    ) {
      response.writeHead(204).end()
      return
    }
    response.writeHead(500).end()
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  t.after(() => new Promise((resolve) => server.close(resolve)))
  const address = server.address()
  assert.ok(address && typeof address !== "string")
  const origin = `http://127.0.0.1:${address.port}`
  const expiresAt = new Date(Date.now() + 120_000).toISOString()
  const resolutions = [
    {
      authorization: "Bearer media-old",
      backend: "mediamtx",
      expiresAt,
      whep: `${origin}/mediamtx?edge=old`,
    },
    {
      authorization: "",
      backend: "direct",
      expiresAt,
      whep: `${origin}/direct?edge=refresh-only`,
    },
    {
      authorization: "",
      backend: "direct",
      expiresAt,
      whep: `${origin}/direct?edge=fresh`,
    },
  ]
  const clients = []
  const failures = []
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: (resolution, callbacks) => {
      const client = new WHEPClient({
        allowInsecureHTTP: true,
        allowLegacyWildcardETag: resolution.backend === "mediamtx",
        authorization: resolution.authorization,
        credentialExpiresAt: resolution.expiresAt,
        endpoint: resolution.whep,
        iceServers: [],
        onError: callbacks.onError,
        onTrack: callbacks.onTrack,
        peerFactory: () => new FakePeer(),
        refreshCredentials: (signal) => callbacks.refresh(signal),
      })
      clients.push(client)
      return client
    },
    delayForAttempt: () => 0,
    disconnectGraceMs: 0,
    onFailure: (cause) => failures.push(cause),
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => {
      const resolution = resolutions.shift()
      assert.ok(resolution)
      return resolution
    },
    stableSessionMs: 60_000,
  })
  await controller.start()
  clients[0].peer.connectionState = "failed"
  clients[0].peer.onconnectionstatechange?.(new Event("connectionstatechange"))
  await eventually(
    () =>
      mediaMTXDeleted &&
      requests.some(
        (request) =>
          request.method === "POST" && request.url === "/direct?edge=fresh",
      ),
  )
  assert.equal(failures.length, 0)
  assert.equal(resolutions.length, 0)
  assert.equal(
    requests.find(
      (request) =>
        request.method === "POST" && request.url === "/mediamtx?edge=old",
    )?.authorization,
    "Bearer media-old",
  )
  assert.equal(
    requests.some((request) => request.url?.includes("refresh-only")),
    false,
  )
  await controller.stop()
})

test("playback failure excludes MediaMTX for the remaining viewer session", async () => {
  const clients = []
  const exclusions = []
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: (resolution, callbacks) => {
      const client = fakeClient(callbacks)
      client.backend = resolution.backend
      client.close = async () => {
        client.closed = true
      }
      clients.push(client)
      return client
    },
    delayForAttempt: () => 0,
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async (_signal, excludedBackend) => {
      exclusions.push(excludedBackend)
      return { backend: excludedBackend === "mediamtx" ? "direct" : "mediamtx" }
    },
    stableSessionMs: 60000,
  })
  await controller.start()
  assert.equal(
    controller.excludeCurrentBackend(new Error("playback stalled")),
    true,
  )
  assert.equal(
    controller.excludeCurrentBackend(new Error("duplicate stall")),
    false,
  )
  await eventually(() => clients.length === 2)
  assert.deepEqual(exclusions, [null, "mediamtx"])
  assert.equal(clients[0].backend, "mediamtx")
  assert.equal(clients[0].closed, true)
  assert.equal(clients[1].backend, "direct")
  clients[1].peer.connectionState = "failed"
  clients[1].peer.onconnectionstatechange?.()
  await eventually(() => clients.length === 3)
  assert.equal(clients[2].backend, "direct")
  assert.deepEqual(exclusions, [null, "mediamtx", "mediamtx"])
  await controller.stop()
})

test("a MediaMTX session failure falls back to direct without retrying the distributor", async () => {
  const clients = []
  const exclusions = []
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: (resolution, callbacks) => {
      const client = fakeClient(callbacks)
      client.backend = resolution.backend
      client.closed = false
      client.close = async () => {
        client.closed = true
      }
      if (resolution.backend === "mediamtx") {
        client.start = async () => {
          throw new Error("distributor unavailable")
        }
      }
      clients.push(client)
      return client
    },
    delayForAttempt: () => 0,
    excludeBackendAfterFailure: (backend) => backend === "mediamtx",
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async (_signal, excludedBackend) => {
      exclusions.push(excludedBackend)
      return { backend: excludedBackend === "mediamtx" ? "direct" : "mediamtx" }
    },
    stableSessionMs: 60000,
  })
  await controller.start()
  await eventually(() => clients.length === 2)
  assert.deepEqual(exclusions, [null, "mediamtx"])
  assert.equal(clients[0].backend, "mediamtx")
  assert.equal(clients[0].closed, true)
  assert.equal(clients[1].backend, "direct")
  await controller.stop()
})

test("a direct session failure keeps direct eligible for bounded retries", async () => {
  const exclusions = []
  let attempts = 0
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: (_resolution, callbacks) => {
      const client = fakeClient(callbacks)
      client.start = async () => {
        attempts += 1
        if (attempts === 1) {
          throw new Error("temporary direct failure")
        }
      }
      return client
    },
    delayForAttempt: () => 0,
    excludeBackendAfterFailure: (backend) => backend === "mediamtx",
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async (_signal, excludedBackend) => {
      exclusions.push(excludedBackend)
      return { backend: "direct" }
    },
    stableSessionMs: 60000,
  })
  await controller.start()
  await eventually(() => attempts === 2)
  assert.deepEqual(exclusions, [null, null])
  await controller.stop()
})

test("an excluded backend returned by the resolver is never started", async () => {
  let clients = 0
  const failures = []
  const controller = new ViewerSessionController({
    backend: (resolution) => resolution.backend,
    createClient: () => {
      clients += 1
      return fakeClient({})
    },
    delayForAttempt: () => 0,
    maximumSessionReconnects: 1,
    onFailure: (cause) => failures.push(cause),
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => ({ backend: "mediamtx" }),
  })
  await controller.start()
  assert.equal(
    controller.excludeCurrentBackend(new Error("playback stalled")),
    true,
  )
  await eventually(() => failures.length === 1)
  assert.equal(clients, 1)
  assert.match(String(failures[0]), /reconnect limit reached/)
  await controller.stop()
})

test("concurrent failures schedule only one replacement session", async () => {
  const clients = []
  let resolutions = 0
  const controller = new ViewerSessionController({
    backend: () => "direct",
    createClient: (_resolution, callbacks) => {
      const client = fakeClient(callbacks)
      clients.push(client)
      return client
    },
    delayForAttempt: () => 0,
    disconnectGraceMs: 0,
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => ({ id: ++resolutions }),
    stableSessionMs: 60000,
  })
  await controller.start()
  clients[0].callbacks.onError(new Error("transport closed"))
  clients[0].callbacks.onError(new Error("duplicate transport callback"))
  clients[0].peer.connectionState = "failed"
  clients[0].peer.onconnectionstatechange?.()
  await eventually(() => clients.length === 2)
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(resolutions, 2)
  assert.equal(clients.length, 2)
  await controller.stop()
})

test("brief disconnects do not consume the ICE restart budget", async () => {
  let restarts = 0
  let client
  const controller = new ViewerSessionController({
    backend: () => "direct",
    createClient: (_resolution, callbacks) => {
      client = fakeClient(callbacks)
      client.restart = async () => {
        restarts += 1
      }
      return client
    },
    disconnectGraceMs: 20,
    maximumICERestarts: 1,
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => ({}),
    stableSessionMs: 60000,
  })
  await controller.start()
  for (let attempt = 0; attempt < 3; attempt += 1) {
    client.peer.connectionState = "disconnected"
    client.peer.onconnectionstatechange?.()
    client.peer.connectionState = "connected"
    client.peer.onconnectionstatechange?.()
  }
  client.peer.connectionState = "failed"
  client.peer.onconnectionstatechange?.()
  await eventually(() => restarts === 1)
  await controller.stop()
})

test("an ICE restart that never restores connectivity falls back to a fresh session", async () => {
  const clients = []
  let resolutions = 0
  let restarts = 0
  const controller = new ViewerSessionController({
    backend: () => "direct",
    createClient: (_resolution, callbacks) => {
      const client = fakeClient(callbacks)
      if (clients.length === 0) {
        client.restart = async () => {
          restarts += 1
          client.peer.connectionState = "connecting"
        }
      }
      clients.push(client)
      return client
    },
    delayForAttempt: () => 0,
    disconnectGraceMs: 0,
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => ({ id: ++resolutions }),
    restartOutcomeTimeoutMs: 20,
    stableSessionMs: 60000,
  })
  await controller.start()
  clients[0].peer.connectionState = "failed"
  clients[0].peer.onconnectionstatechange?.()
  await eventually(() => clients.length === 2)
  assert.equal(restarts, 1)
  assert.equal(resolutions, 2)
  await controller.stop()
})

test("stopping aborts a pending resolution without creating a client", async () => {
  let aborted = false
  let clients = 0
  const controller = new ViewerSessionController({
    backend: () => "direct",
    createClient: () => {
      clients += 1
      return fakeClient({})
    },
    onFailure: assert.fail,
    onPhase: () => {},
    onTrack: () => {},
    resolve: (signal) =>
      new Promise((_resolve, reject) => {
        signal.addEventListener(
          "abort",
          () => {
            aborted = true
            reject(signal.reason)
          },
          { once: true },
        )
      }),
  })
  const start = controller.start()
  await controller.stop()
  await start
  assert.equal(aborted, true)
  assert.equal(clients, 0)
})

test("an invalid reconnect delay fails closed without an unhandled rejection", async () => {
  let callbacks
  const failures = []
  const controller = new ViewerSessionController({
    backend: () => "direct",
    createClient: (_resolution, nextCallbacks) => {
      callbacks = nextCallbacks
      return fakeClient(nextCallbacks)
    },
    delayForAttempt: () => Number.NaN,
    onFailure: (cause) => failures.push(cause),
    onPhase: () => {},
    onTrack: () => {},
    resolve: async () => ({}),
  })
  await controller.start()
  callbacks.onError(new Error("transport closed"))
  await eventually(() => failures.length === 1)
  assert.match(String(failures[0]), /reconnect delay must be a non-negative/)
  await controller.stop()
})

function fakeClient(callbacks) {
  return {
    callbacks,
    close: async () => {},
    peer: { connectionState: "connected", onconnectionstatechange: null },
    restart: async () => {},
    start: async () => {},
  }
}

class FakePeer {
  connectionState = "new"
  localDescription = null
  onconnectionstatechange = null
  onicecandidate = null
  ontrack = null
  remoteDescription = null
  signalingState = "stable"
  configuration = { bundlePolicy: "max-bundle", iceServers: [] }

  addTransceiver() {}

  close() {
    this.connectionState = "closed"
  }

  async createOffer() {
    return { type: "offer", sdp: whepOffer }
  }

  getConfiguration() {
    return this.configuration
  }

  setConfiguration(configuration) {
    this.configuration = configuration
  }

  async setLocalDescription(description) {
    this.localDescription = description
    queueMicrotask(() => this.onicecandidate?.({ candidate: null }))
  }

  async setRemoteDescription(description) {
    this.remoteDescription = description
  }
}

const whepOffer = [
  "v=0",
  "o=- 0 0 IN IP4 127.0.0.1",
  "s=-",
  "t=0 0",
  "a=group:BUNDLE 0",
  "a=ice-options:trickle",
  "m=video 9 UDP/TLS/RTP/SAVPF 96",
  "c=IN IP4 0.0.0.0",
  "a=mid:0",
  "a=ice-ufrag:viewer",
  "a=ice-pwd:viewer-password",
  "a=rtcp-mux",
  "a=recvonly",
  "",
].join("\r\n")

const whepAnswer = [
  "v=0",
  "o=- 0 0 IN IP4 127.0.0.1",
  "s=-",
  "t=0 0",
  "a=group:BUNDLE 0",
  "a=ice-options:trickle",
  "m=video 9 UDP/TLS/RTP/SAVPF 96",
  "c=IN IP4 0.0.0.0",
  "a=mid:0",
  "a=ice-ufrag:server",
  "a=ice-pwd:server-password",
  "a=rtcp-mux",
  "a=rtcp-mux-only",
  "a=sendonly",
  "",
].join("\r\n")

async function requestBody(request) {
  const chunks = []
  for await (const chunk of request) {
    chunks.push(chunk)
  }
  return Buffer.concat(chunks).toString("utf8")
}

async function eventually(predicate, timeoutMs = 1000) {
  const deadline = Date.now() + timeoutMs
  while (!predicate()) {
    assert.ok(Date.now() < deadline, "condition did not become true")
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}
