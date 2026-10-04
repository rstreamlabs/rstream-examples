import assert from "node:assert/strict"
import { createServer } from "node:http"
import { getEventListeners } from "node:events"
import { generateKeyPairSync } from "node:crypto"
import test from "node:test"
import {
  boundedFetch,
  coalesceInFlight,
  platformRstreamClientFactory,
  requestScopedClient,
} from "../src/lib/bounded-rstream.ts"

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}

const projectFixture = {
  id: "project",
  workspaceId: "workspace",
  name: "Qualification",
  endpoint: "endpoint",
  url: "engine.test",
  domain: "engine.test",
  enginePort: 443,
  turnRealm: "realm",
  turnPort: 3478,
  turnsPort: 5349,
  status: "active",
  routing: "regional",
  provider: "other",
  plan: "pro",
  deployment: "shared",
}

test("overlapping operations share work but retain independent cancellation", async () => {
  const ready = deferred()
  let calls = 0,
    upstream
  const load = coalesceInFlight((signal) => {
    calls++
    upstream = signal
    return ready.promise
  })
  const left = new AbortController(),
    right = new AbortController()
  const a = load(left.signal),
    b = load(right.signal)
  await Promise.resolve()
  assert.equal(calls, 1)
  left.abort(new Error("left departed"))
  await assert.rejects(a, /left departed/)
  assert.equal(upstream.aborted, false)
  ready.resolve("resolved")
  assert.equal(await b, "resolved")
  for (const signal of [left.signal, right.signal])
    assert.equal(getEventListeners(signal, "abort").length, 0)
  assert.equal(await load(), "resolved")
  assert.equal(calls, 2, "Completed work must not be reused")
})

test("the last departure cancels work and late completion cannot replace a new flight", async () => {
  const operations = []
  const load = coalesceInFlight((signal) => {
    const operation = { ...deferred(), signal }
    operations.push(operation)
    return operation.promise
  })
  const left = new AbortController(),
    right = new AbortController()
  const a = load(left.signal),
    b = load(right.signal)
  const rejected = Promise.all([
    assert.rejects(a, /departed/),
    assert.rejects(b, /departed/),
  ])
  await Promise.resolve()
  left.abort(new Error("left departed"))
  right.abort(new Error("right departed"))
  await rejected
  assert.equal(operations[0].signal.aborted, true)
  const next = load()
  await Promise.resolve()
  operations[0].resolve("obsolete")
  await Promise.resolve()
  await Promise.resolve()
  const join = load()
  operations[1].resolve("fresh")
  assert.deepEqual(await Promise.all([next, join]), ["fresh", "fresh"])
  assert.equal(operations.length, 2)
})

test("failure, pre-cancellation and the completion/cancel race leave no shared state", async () => {
  const ready = deferred()
  let calls = 0
  const load = coalesceInFlight(async () => {
    if (++calls === 1) return ready.promise
    return "retry"
  })
  const aborted = new AbortController()
  aborted.abort(new Error("already stopped"))
  await assert.rejects(load(aborted.signal), /already stopped/)
  assert.equal(calls, 0)
  const left = load(),
    right = load()
  const failures = Promise.all([
    assert.rejects(left, /unavailable/),
    assert.rejects(right, /unavailable/),
  ])
  ready.reject(new Error("unavailable"))
  await failures
  assert.equal(await load(), "retry")
  assert.equal(calls, 2)
  const racing = new AbortController()
  const completion = load(racing.signal)
  racing.abort(new Error("canceled before delivery"))
  await assert.rejects(completion, /canceled before delivery/)
  assert.equal(getEventListeners(racing.signal, "abort").length, 0)
  assert.equal(await load(), "retry")
})

test("the bounded upstream deadline rejects all subscribers and the next request retries", async () => {
  let count = 0
  const server = await serverFor((_req, res) => {
    if (++count > 1) res.end("recovered")
  })
  const load = coalesceInFlight(async (signal) =>
    (await boundedFetch(signal, 100, 100)(server.url)).text(),
  )
  try {
    await Promise.all([
      assert.rejects(load(), /timed out|aborted/),
      assert.rejects(load(), /timed out|aborted/),
    ])
    assert.equal(count, 1)
    assert.equal(await load(), "recovered")
    assert.equal(count, 2)
  } finally {
    await server.close()
  }
})

test("project resolution coalesces live requests, preserves cancellation and never caches completion", async () => {
  const requests = []
  const server = await serverFor((_req, res) => {
    requests.push(() => res.end(JSON.stringify(projectFixture)))
  })
  const options = {
    apiUrl: server.url,
    credentials: {
      clientId: "qualification",
      clientSecret: generateKeyPairSync("ec", { namedCurve: "secp521r1" })
        .privateKey.export({ type: "pkcs8", format: "der" })
        .toString("hex"),
    },
    projectEndpoint: "endpoint",
    projectId: "project",
  }
  const create = platformRstreamClientFactory()
  const first = new AbortController(),
    second = new AbortController()
  const pending = []
  const start = (...args) => {
    const promise = create(...args)
    promise.catch(() => {})
    pending.push(promise)
    return promise
  }
  try {
    const canceled = start(options, first.signal)
    const surviving = start(options, second.signal)
    await waitFor(() => requests.length === 1)
    first.abort(new Error("viewer closed"))
    await assert.rejects(canceled, /viewer closed/)
    requests[0]()
    const client = await surviving
    assert.equal(await client.getEngine(), "endpoint.engine.test:443")
    assert.equal((await client.getTURNTarget()).turnRealm, "realm")
    const fresh = start(options, second.signal)
    await waitFor(() => requests.length === 2)
    requests[1]()
    assert.notEqual(await fresh, client)
    const mismatched = assert.rejects(
      start({ ...options, projectId: "different" }, second.signal),
      /different projects/,
    )
    await waitFor(() => requests.length === 3)
    requests[2]()
    await mismatched
  } finally {
    first.abort()
    second.abort()
    await Promise.allSettled(pending)
    await server.close()
  }
})

test("project endpoint, credential or control-plane changes never join another configuration", async () => {
  const requests = []
  const server = await serverFor((_req, res) => {
    requests.push(() => res.end(JSON.stringify(projectFixture)))
  })
  const options = {
    apiUrl: server.url,
    credentials: {
      clientId: "qualification",
      clientSecret: generateKeyPairSync("ec", { namedCurve: "secp521r1" })
        .privateKey.export({ type: "pkcs8", format: "der" })
        .toString("hex"),
    },
    projectEndpoint: "endpoint",
  }
  const parent = new AbortController()
  const create = platformRstreamClientFactory()
  const pending = []
  const start = (options) => {
    const promise = create(options, parent.signal)
    promise.catch(() => {})
    pending.push(promise)
    return promise
  }
  try {
    for (const changed of [
      { ...options, projectEndpoint: "another" },
      { ...options, apiUrl: `${server.url}/another-base` },
      {
        ...options,
        credentials: { ...options.credentials, clientId: "another" },
      },
      {
        ...options,
        credentials: {
          ...options.credentials,
          clientSecret: generateKeyPairSync("ec", { namedCurve: "secp521r1" })
            .privateKey.export({ type: "pkcs8", format: "der" })
            .toString("hex"),
        },
      },
    ]) {
      const before = requests.length
      const original = start(options)
      const replacement = start(changed)
      await waitFor(() => requests.length === before + 2)
      requests[before]()
      await original
      const join = start(changed)
      requests[before + 1]()
      const [left, right] = await Promise.all([replacement, join])
      assert.notEqual(
        left,
        right,
        "Only metadata is shared, never request clients",
      )
      assert.equal(requests.length, before + 2)
    }
  } finally {
    parent.abort()
    await Promise.allSettled(pending)
    await server.close()
  }
})

async function waitFor(ready) {
  const deadline = Date.now() + 2000
  while (!ready() && Date.now() < deadline)
    await new Promise((resolve) => setTimeout(resolve, 1))
  assert.ok(ready(), "Expected upstream operation before deadline")
}

test("request client resolution is shared only within one live request", async () => {
  let calls = 0
  const client = requestScopedClient(async () => ({ instance: ++calls }))
  const first = new AbortController()
  const [left, right] = await Promise.all([
    client(first.signal),
    client(first.signal),
  ])
  assert.equal(left, right)
  assert.equal(await client(first.signal), left)
  assert.equal(calls, 1)
  assert.notEqual(await client(new AbortController().signal), left)
  assert.notEqual(
    await client(),
    await client(),
    "Unscoped operations are never shared",
  )
  first.abort(new Error("request ended"))
  await assert.rejects(client(first.signal), /request ended/)
  assert.equal(
    calls,
    4,
    "Cancellation cannot return a previously resolved client",
  )
})

test("canceling shared resolution rejects every waiter without poisoning another request", async () => {
  let calls = 0
  const server = await serverFor((_req, res) => {
    calls++
    if (calls > 1) res.end("ready")
  })
  const client = requestScopedClient(async (signal) =>
    (await boundedFetch(signal)(server.url)).text(),
  )
  const parent = new AbortController()
  try {
    const left = client(parent.signal),
      right = client(parent.signal)
    const rejected = Promise.all([
      assert.rejects(left, /caller stopped/),
      assert.rejects(right, /caller stopped/),
    ])
    const deadline = Date.now() + 2000
    while (calls === 0 && Date.now() < deadline)
      await new Promise((resolve) => setTimeout(resolve, 1))
    parent.abort(new Error("caller stopped"))
    await rejected
    assert.equal(calls, 1)
    assert.equal(await client(new AbortController().signal), "ready")
    assert.equal(calls, 2)
  } finally {
    parent.abort()
    await server.close()
  }
})

test("failed resolution is shared within its request and retried only by a new request", async () => {
  let calls = 0
  const client = requestScopedClient(async () => {
    if (++calls === 1) throw new Error("resolution failed")
    return "new request"
  })
  const signal = new AbortController().signal
  await Promise.all([
    assert.rejects(client(signal), /resolution failed/),
    assert.rejects(client(signal), /resolution failed/),
  ])
  assert.equal(calls, 1)
  assert.equal(await client(new AbortController().signal), "new request")
})

test("cancellation winning against successful resolution never returns a client", async () => {
  let finish
  const client = requestScopedClient(
    () =>
      new Promise((resolve) => {
        finish = resolve
      }),
  )
  const parent = new AbortController()
  const pending = client(parent.signal)
  parent.abort(new Error("caller left"))
  finish({ ready: true })
  await assert.rejects(pending, /caller left/)
})

async function serverFor(handler) {
  const server = createServer(handler)
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    close: async () => {
      server.closeAllConnections()
      await new Promise((resolve) => server.close(resolve))
    },
  }
}

test("bounded SDK fetch covers stalled headers and body, and honors parent cancellation", async () => {
  for (const headers of [false, true]) {
    const server = await serverFor((_req, res) => {
      if (headers) {
        res.writeHead(200)
        res.write("{")
      }
    })
    try {
      await assert.rejects(
        boundedFetch(undefined, 100, 20)(server.url),
        /timed out|aborted/,
      )
    } finally {
      await server.close()
    }
  }
  const controller = new AbortController()
  controller.abort(new Error("caller stopped"))
  await assert.rejects(
    boundedFetch(controller.signal)("http://127.0.0.1:1"),
    /caller stopped/,
  )
})

test("bounded SDK fetch rejects redirects, oversized bodies and upstream error contents", async () => {
  for (const [status, body, pattern] of [
    [302, "", /redirect|fetch failed/],
    [200, "x".repeat(200), /limit/],
    [500, "upstream-secret", /failed \(500\)/],
  ]) {
    const server = await serverFor((_req, res) => {
      res.writeHead(status, { Location: "http://127.0.0.1:1" })
      res.end(body)
    })
    try {
      await assert.rejects(
        boundedFetch(undefined, 100)(server.url),
        (error) => {
          assert.match(error.message, pattern)
          assert.doesNotMatch(error.message, /upstream-secret/)
          return true
        },
      )
    } finally {
      await server.close()
    }
  }
})

test("bounded SDK fetch returns the complete validated-size response", async () => {
  const server = await serverFor((_req, res) => {
    res.writeHead(200, { "content-type": "application/json" })
    res.end('{"ok":true}')
  })
  try {
    assert.deepEqual(await (await boundedFetch()(server.url)).json(), {
      ok: true,
    })
  } finally {
    await server.close()
  }
})
