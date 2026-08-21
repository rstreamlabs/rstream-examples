import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { createServer } from "node:http"
import { mkdtemp, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { setTimeout as delay } from "node:timers/promises"

import { RstreamTunnelsClient } from "@rstreamlabs/tunnels"

const startupTimeoutMilliseconds = 30_000
const shutdownTimeoutMilliseconds = 10_000
const requestTimeoutMilliseconds = 10_000
const shortTokenTTLSeconds = 5
const refreshedTokenTTLSeconds = 30

const requiredEnvironment = [
  "RSTREAM_CLIENT_ID",
  "RSTREAM_CLIENT_SECRET",
  "RSTREAM_EDGE_AUTH_EXPECTED_ENGINE",
  "RSTREAM_PROJECT_ENDPOINT",
]

for (const name of requiredEnvironment) {
  if (!process.env[name]?.trim()) {
    throw new Error(`${name} is required`)
  }
}

const runtimeDirectory = await mkdtemp(
  join(tmpdir(), "rstream-edge-auth-qualification-"),
)
const tunnelName = `whep-edge-auth-${process.pid}-${Date.now()}`
const upstreamRequests = []
const upstream = createServer((request, response) => {
  upstreamRequests.push({
    authorization: request.headers.authorization ?? null,
    method: request.method,
    url: request.url,
  })
  if (request.headers.authorization !== "Bearer application-token") {
    response.writeHead(401).end()
    return
  }
  if (request.method === "POST" && request.url === "/whep") {
    response.writeHead(201, {
      "Access-Control-Expose-Headers": "ETag, Location",
      "Content-Type": "application/sdp",
      ETag: '"session-1"',
      Location: "/whep/session-1",
    })
    response.end("v=0\r\n")
    return
  }
  if (request.method === "PATCH" && request.url === "/whep/session-1") {
    response.writeHead(204, { ETag: '"session-2"' }).end()
    return
  }
  if (request.method === "DELETE" && request.url === "/whep/session-1") {
    response.writeHead(200).end()
    return
  }
  response.writeHead(404).end()
})

let forward = null
try {
  const upstreamAddress = await listen(upstream)
  const client = new RstreamTunnelsClient({
    apiUrl: process.env.RSTREAM_API_URL,
    credentials: {
      clientId: process.env.RSTREAM_CLIENT_ID,
      clientSecret: process.env.RSTREAM_CLIENT_SECRET,
    },
    projectEndpoint: process.env.RSTREAM_PROJECT_ENDPOINT,
  })
  const engine = await client.getEngine()
  assert.equal(
    canonicalEngine(engine),
    canonicalEngine(process.env.RSTREAM_EDGE_AUTH_EXPECTED_ENGINE),
    "resolved engine does not match RSTREAM_EDGE_AUTH_EXPECTED_ENGINE",
  )
  const createCredential = await client.auth.createAuthToken({
    expires_in: 300,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            create: {
              filters: {
                name: { exact: tunnelName },
                protocol: "http",
                publish: true,
                token_auth: true,
              },
            },
          },
        },
      },
    },
  })
  const configPath = join(runtimeDirectory, "config.json")
  await writeFile(configPath, '{"version":1}\n', { mode: 0o600 })
  const childEnvironment = qualificationEnvironment(
    engine,
    createCredential.token,
  )
  forward = spawn(
    process.env.RSTREAM_BIN ?? "rstream",
    [
      "--config",
      configPath,
      "forward",
      `127.0.0.1:${upstreamAddress.port}`,
      "--http",
      "--publish",
      "--token-auth",
      "--no-retry",
      "--name",
      tunnelName,
      "--output",
      "none",
    ],
    { env: childEnvironment, stdio: ["ignore", "ignore", "pipe"] },
  )
  const forwardFailure = captureChildFailure(forward)
  const tunnel = await waitForTunnel(client, tunnelName, forwardFailure)
  const host = tunnel.host ?? tunnel.hostname
  assert.equal(typeof host, "string", "published tunnel has no host")
  const endpoint = new URL(`https://${host}/whep`)
  const shortCredential = await connectCredential(
    client,
    tunnel,
    shortTokenTTLSeconds,
  )
  const sessionResponse = await edgeRequest(endpoint, shortCredential, {
    body: "v=0\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/sdp",
    },
    method: "POST",
  })
  assert.equal(sessionResponse.status, 201)
  const location = sessionResponse.headers.get("location")
  assert.ok(location, "WHEP response omitted Location")
  const session = new URL(location, endpoint)
  session.searchParams.set("rstream.token", shortCredential)
  assert.deepEqual(upstreamRequests, [
    {
      authorization: "Bearer application-token",
      method: "POST",
      url: "/whep",
    },
  ])

  await waitUntilExpired(shortCredential)
  const requestsBeforeExpiryChecks = upstreamRequests.length
  const expiredPatch = await edgeRequest(session, null, {
    body: "a=ice-frag:test\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/trickle-ice-sdpfrag",
      "If-Match": '"session-1"',
    },
    method: "PATCH",
  })
  assert.equal(expiredPatch.status, 401)
  const expiredDelete = await edgeRequest(session, null, {
    headers: { Authorization: "Bearer application-token" },
    method: "DELETE",
  })
  assert.equal(expiredDelete.status, 401)
  assert.equal(upstreamRequests.length, requestsBeforeExpiryChecks)

  const refreshedCredential = await connectCredential(
    client,
    tunnel,
    refreshedTokenTTLSeconds,
  )
  session.searchParams.set("rstream.token", refreshedCredential)
  const refreshedPatch = await edgeRequest(session, null, {
    body: "a=ice-frag:test\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/trickle-ice-sdpfrag",
      "If-Match": '"session-1"',
    },
    method: "PATCH",
  })
  assert.equal(refreshedPatch.status, 204)
  const refreshedDelete = await edgeRequest(session, null, {
    headers: { Authorization: "Bearer application-token" },
    method: "DELETE",
  })
  assert.equal(refreshedDelete.status, 200)
  assert.deepEqual(upstreamRequests.slice(-2), [
    {
      authorization: "Bearer application-token",
      method: "PATCH",
      url: "/whep/session-1",
    },
    {
      authorization: "Bearer application-token",
      method: "DELETE",
      url: "/whep/session-1",
    },
  ])
  process.stdout.write(
    "rstream rejected expired WHEP lifecycle credentials and accepted a bound refresh.\n",
  )
} finally {
  await stopChild(forward)
  await closeServer(upstream)
  await rm(runtimeDirectory, { force: true, recursive: true })
}

async function connectCredential(client, tunnel, ttlSeconds) {
  const credential = await client.auth.createAuthToken({
    expires_in: ttlSeconds,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            connect: {
              filters: {
                id: tunnel.id,
                protocol: "http",
                publish: true,
                status: "online",
                token_auth: true,
              },
              params: { path: { regex: "^/whep(?:/[^/?#]{1,256})?$" } },
            },
          },
        },
      },
    },
  })
  return credential.token
}

async function edgeRequest(endpoint, credential, init) {
  const url = new URL(endpoint)
  if (credential) {
    url.searchParams.set("rstream.token", credential)
  }
  return fetch(url, {
    ...init,
    credentials: "omit",
    redirect: "manual",
    signal: AbortSignal.timeout(requestTimeoutMilliseconds),
  })
}

async function waitUntilExpired(token) {
  const payload = JSON.parse(
    Buffer.from(token.split(".")[1], "base64url").toString("utf8"),
  )
  assert.equal(typeof payload.exp, "number")
  const remaining = payload.exp * 1000 - Date.now()
  if (remaining >= 0) {
    await delay(remaining + 1_100)
  }
}

async function waitForTunnel(client, name, childFailure) {
  const deadline = Date.now() + startupTimeoutMilliseconds
  while (Date.now() < deadline) {
    const failure = await Promise.race([
      childFailure,
      delay(250).then(() => null),
    ])
    if (failure) {
      throw failure
    }
    const tunnels = await client.tunnels.list({
      filters: {
        name,
        protocol: "http",
        publish: true,
        status: "online",
      },
      limit: 20,
    })
    if (tunnels.length > 0) {
      return tunnels[0]
    }
  }
  throw new Error("rstream tunnel did not become ready before the deadline")
}

function captureChildFailure(child) {
  let stderr = ""
  child.stderr.on("data", (chunk) => {
    if (stderr.length < 16_384) {
      stderr += chunk.toString("utf8").slice(0, 16_384 - stderr.length)
    }
  })
  return new Promise((resolve) => {
    child.once("error", (error) => resolve(error))
    child.once("exit", (code, signal) => {
      resolve(
        new Error(
          `rstream forward exited before readiness (code=${code}, signal=${signal}, stderr=${sanitize(stderr)})`,
        ),
      )
    })
  })
}

function qualificationEnvironment(engine, token) {
  const environment = {
    HOME: process.env.HOME,
    LANG: process.env.LANG,
    PATH: process.env.PATH,
    RSTREAM_AUTHENTICATION_TOKEN: token,
    RSTREAM_ENGINE: engine,
    TMPDIR: process.env.TMPDIR,
  }
  return Object.fromEntries(
    Object.entries(environment).filter(([, value]) => value !== undefined),
  )
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject)
      resolve(server.address())
    })
  })
}

function closeServer(server) {
  return new Promise((resolve) => server.close(() => resolve()))
}

async function stopChild(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) {
    return
  }
  child.kill("SIGTERM")
  const exited = await Promise.race([
    new Promise((resolve) => child.once("exit", () => resolve(true))),
    delay(shutdownTimeoutMilliseconds).then(() => false),
  ])
  if (!exited) {
    child.kill("SIGKILL")
    await new Promise((resolve) => child.once("exit", resolve))
  }
}

function sanitize(value) {
  return value.replaceAll(/Bearer\s+\S+/gi, "Bearer [redacted]").trim()
}

function canonicalEngine(value) {
  const endpoint = new URL(value.includes("://") ? value : `https://${value}`)
  const port = endpoint.port || (endpoint.protocol === "https:" ? "443" : "80")
  return `${endpoint.hostname.toLowerCase()}:${port}`
}
