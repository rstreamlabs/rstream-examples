import assert from "node:assert/strict"
import { createServer } from "node:http"
import test from "node:test"
import {
  boundedFetch,
  requestScopedClient,
} from "../src/lib/bounded-rstream.ts"

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
