import assert from "node:assert/strict"
import { createServer } from "node:http"
import test from "node:test"
import { boundedFetch } from "../src/lib/bounded-rstream.ts"

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
