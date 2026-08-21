import assert from "node:assert/strict"
import { createSocket } from "node:dgram"
import { EventEmitter } from "node:events"
import { createServer } from "node:net"
import test from "node:test"

import { generateMediaMTXKeys } from "./generate-mediamtx-key.mjs"
import {
  localStackOptions,
  localTunnelNames,
  installStopSignalHandlers,
  mediaMTXEnvironment,
  requireAvailablePort,
  requireAvailableUDPPort,
  tunnelConfiguration,
  tunnelResources,
} from "./run-local-mediamtx.mjs"

test("local MediaMTX stack owns and releases all stop signal handlers", async () => {
  const target = new EventEmitter()
  const stop = installStopSignalHandlers(target)
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    assert.equal(target.listenerCount(signal), 1)
  }
  target.emit("SIGINT")
  target.emit("SIGTERM")
  assert.equal(await stop.promise, "SIGINT")
  stop.remove()
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    assert.equal(target.listenerCount(signal), 0)
  }
})

test("local MediaMTX stack defaults to direct public exposure", () => {
  assert.deepEqual(localStackOptions([]), { exposure: "public" })
  assert.deepEqual(localStackOptions(["--exposure", "rstream"]), {
    exposure: "rstream",
  })
  assert.throws(() => localStackOptions(["--exposure", "invalid"]), /usage:/)
})

test("local MediaMTX stack keeps its rstream tunnels distinct", () => {
  const names = localTunnelNames("test-42")
  assert.deepEqual(names, {
    distributor: "webrtc-video-mediamtx-test-42",
    platform: "webrtc-video-platform-test-42",
  })
  assert.deepEqual(tunnelResources(names, "rstream"), {
    tunnels: {
      OR: [
        {
          scopes: {
            tunnels: {
              create: {
                filters: {
                  name: { exact: names.platform },
                  protocol: "http",
                  publish: true,
                  token_auth: false,
                },
              },
            },
          },
        },
        {
          scopes: {
            tunnels: {
              create: {
                filters: {
                  name: { exact: names.distributor },
                  protocol: "http",
                  publish: true,
                  token_auth: true,
                },
              },
            },
          },
        },
      ],
    },
  })
  const configuration = tunnelConfiguration(names, "rstream")
  assert.match(configuration, /forward: 127\.0\.0\.1:3000/)
  assert.match(configuration, /forward: 127\.0\.0\.1:8889/)
  assert.match(configuration, /token: false/)
  assert.match(configuration, /token: true/)
})

test("public MediaMTX exposure creates only the platform callback tunnel", () => {
  const names = localTunnelNames("test-public")
  const resources = tunnelResources(names, "public")
  assert.equal(resources.tunnels.OR.length, 1)
  assert.equal(
    resources.tunnels.OR[0].scopes.tunnels.create.filters.name.exact,
    names.platform,
  )
  const configuration = tunnelConfiguration(names, "public")
  assert.match(configuration, /forward: 127\.0\.0\.1:3000/)
  assert.doesNotMatch(configuration, /forward: 127\.0\.0\.1:8889/)
})

test("local MediaMTX stack refuses to reuse an occupied port", async () => {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen({ host: "127.0.0.1", port: 0 }, resolve)
  })
  const address = server.address()
  assert.ok(address && typeof address === "object")
  await assert.rejects(requireAvailablePort(address.port), /already in use/)
  await new Promise((resolve, reject) => {
    server.close((err) => (err ? reject(err) : resolve()))
  })
  await requireAvailablePort(address.port)
})

test("local MediaMTX stack refuses to reuse an occupied UDP port", async () => {
  const socket = createSocket("udp4")
  await new Promise((resolve, reject) => {
    socket.once("error", reject)
    socket.bind(0, "127.0.0.1", resolve)
  })
  const address = socket.address()
  assert.ok(address && typeof address === "object")
  await assert.rejects(requireAvailableUDPPort(address.port), /already in use/)
  await new Promise((resolve) => socket.close(resolve))
  await requireAvailableUDPPort(address.port)
})

test("local MediaMTX stack uses HTTPS for every control-plane callback", () => {
  const keys = generateMediaMTXKeys("test-instance")
  const environment = mediaMTXEnvironment("platform.example", keys)
  assert.equal(
    environment.MTX_AUTHJWTJWKS,
    "https://platform.example/api/video/distributor/jwks",
  )
  assert.equal(
    environment.RSTREAM_SOURCE_RESOLVER_URL,
    "https://platform.example/api/video/distributor/source",
  )
  assert.equal(environment.MTX_WEBRTCALLOWORIGINS, "http://localhost:3000")
  assert.equal(
    environment.RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64,
    keys.RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64,
  )
})
