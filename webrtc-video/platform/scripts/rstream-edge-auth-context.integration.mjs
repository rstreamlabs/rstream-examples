import assert from "node:assert/strict"
import { execFile, spawn } from "node:child_process"
import { createServer } from "node:http"
import { promisify } from "node:util"
import { setTimeout as delay } from "node:timers/promises"

const execute = promisify(execFile)
const context = requiredEnvironment("RSTREAM_EDGE_AUTH_CONTEXT")
const expectedEngine = requiredEnvironment("RSTREAM_EDGE_AUTH_EXPECTED_ENGINE")
const projectID = requiredEnvironment("RSTREAM_EDGE_AUTH_PROJECT_ID")
const shortTokenTTLSeconds = 5
const refreshedTokenTTLSeconds = 30
const tunnelName = `whep-edge-auth-${process.pid}-${Date.now()}`
const upstreamRequests = []
const upstream = createServer((request, response) => {
  upstreamRequests.push({
    authorization: request.headers.authorization ?? null,
    method: request.method,
    url: request.url,
  })
  if (
    request.headers.authorization !== undefined &&
    request.headers.authorization !== "Bearer application-token"
  ) {
    response.writeHead(401).end()
    return
  }
  if (request.method === "POST" && request.url === "/whep") {
    response
      .writeHead(201, { ETag: '"one"', Location: "/whep/session" })
      .end("v=0\r\n")
    return
  }
  if (request.method === "PATCH" && request.url === "/whep/session") {
    response.writeHead(204).end()
    return
  }
  if (request.method === "DELETE" && request.url === "/whep/session") {
    response.writeHead(200).end()
    return
  }
  response.writeHead(404).end()
})

await validateTarget()
let forward = null
try {
  const address = await listen(upstream)
  const createToken = await delegatedToken(
    {
      tunnels: {
        projects: [projectID],
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
    "tunnels.tunnels.create-delete",
  )
  forward = spawn(
    process.env.RSTREAM_BIN ?? "rstream",
    [
      "--context",
      context,
      "forward",
      `127.0.0.1:${address.port}`,
      "--http",
      "--publish",
      "--token-auth",
      "--no-retry",
      "--name",
      tunnelName,
      "--output",
      "none",
    ],
    {
      env: { ...process.env, RSTREAM_AUTHENTICATION_TOKEN: createToken },
      stdio: ["ignore", "ignore", "pipe"],
    },
  )
  const failure = captureChildFailure(forward)
  const tunnel = await waitForTunnel(failure)
  const connectToken = await delegatedToken(
    connectResources(tunnel.id),
    "tunnels.streams.create-delete",
  )
  const host = tunnel.host ?? tunnel.hostname
  assert.equal(typeof host, "string", "published tunnel has no host")
  const base = new URL(`https://${host}`)
  const endpoint = edgeURL(new URL("/whep", base), connectToken)
  const created = await request(endpoint, {
    body: "v=0\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/sdp",
    },
    method: "POST",
  })
  assert.equal(created.status, 201)
  const location = created.headers.get("location")
  assert.ok(location, "upstream response omitted Location")
  const session = edgeURL(new URL(location, endpoint), connectToken)
  const patched = await request(session, {
    body: "a=ice-frag:test\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/trickle-ice-sdpfrag",
      "If-Match": '"one"',
    },
    method: "PATCH",
  })
  assert.equal(patched.status, 204)
  const requestsBeforeRejection = upstreamRequests.length
  for (const path of ["/ws", "/api/status", "/metrics", "/admin"]) {
    const outsideScope = edgeURL(new URL(path, base), connectToken)
    assert.equal(
      (
        await request(outsideScope, {
          headers: { Authorization: "Bearer application-token" },
          method: "GET",
        })
      ).status,
      403,
      `${path} escaped the WHEP-only credential scope`,
    )
    assert.equal(upstreamRequests.length, requestsBeforeRejection)
  }
  assert.equal(
    (
      await request(new URL("/whep", base), {
        headers: { Authorization: "Bearer application-token" },
        method: "POST",
      })
    ).status,
    401,
  )
  assert.equal(upstreamRequests.length, requestsBeforeRejection)
  const ambiguous = new URL(endpoint)
  ambiguous.searchParams.append("rstream.token", connectToken)
  assert.equal(
    (
      await request(ambiguous, {
        headers: { Authorization: "Bearer application-token" },
        method: "POST",
      })
    ).status,
    401,
  )
  assert.equal(upstreamRequests.length, requestsBeforeRejection)
  assert.equal(
    (
      await request(session, {
        headers: { Authorization: "Bearer application-token" },
        method: "DELETE",
      })
    ).status,
    200,
  )
  const edgeOnlyCreated = await request(endpoint, {
    body: "v=0\r\n",
    headers: { "Content-Type": "application/sdp" },
    method: "POST",
  })
  assert.equal(edgeOnlyCreated.status, 201)
  const edgeOnlyLocation = edgeOnlyCreated.headers.get("location")
  assert.ok(edgeOnlyLocation, "edge-only response omitted Location")
  const edgeOnlySession = edgeURL(
    new URL(edgeOnlyLocation, endpoint),
    connectToken,
  )
  assert.equal(
    (
      await request(edgeOnlySession, {
        body: "a=ice-frag:test\r\n",
        headers: {
          "Content-Type": "application/trickle-ice-sdpfrag",
          "If-Match": '"one"',
        },
        method: "PATCH",
      })
    ).status,
    204,
  )
  assert.equal(
    (await request(edgeOnlySession, { method: "DELETE" })).status,
    200,
  )
  const shortConnectToken = await delegatedToken(
    connectResources(tunnel.id),
    "tunnels.streams.create-delete",
    shortTokenTTLSeconds,
  )
  const shortEndpoint = edgeURL(new URL("/whep", base), shortConnectToken)
  const shortCreated = await request(shortEndpoint, {
    body: "v=0\r\n",
    headers: {
      Authorization: "Bearer application-token",
      "Content-Type": "application/sdp",
    },
    method: "POST",
  })
  assert.equal(shortCreated.status, 201)
  const shortLocation = shortCreated.headers.get("location")
  assert.ok(shortLocation, "short-lived response omitted Location")
  const shortSession = edgeURL(
    new URL(shortLocation, shortEndpoint),
    shortConnectToken,
  )
  await waitUntilExpired(shortConnectToken)
  const requestsBeforeExpiryChecks = upstreamRequests.length
  assert.equal(
    (
      await request(shortSession, {
        body: "a=ice-frag:test\r\n",
        headers: {
          Authorization: "Bearer application-token",
          "Content-Type": "application/trickle-ice-sdpfrag",
          "If-Match": '"one"',
        },
        method: "PATCH",
      })
    ).status,
    401,
  )
  assert.equal(
    (
      await request(shortSession, {
        headers: { Authorization: "Bearer application-token" },
        method: "DELETE",
      })
    ).status,
    401,
  )
  assert.equal(upstreamRequests.length, requestsBeforeExpiryChecks)
  const refreshedConnectToken = await delegatedToken(
    connectResources(tunnel.id),
    "tunnels.streams.create-delete",
    refreshedTokenTTLSeconds,
  )
  const refreshedSession = edgeURL(
    new URL(shortLocation, shortEndpoint),
    refreshedConnectToken,
  )
  assert.equal(
    (
      await request(refreshedSession, {
        body: "a=ice-frag:test\r\n",
        headers: {
          Authorization: "Bearer application-token",
          "Content-Type": "application/trickle-ice-sdpfrag",
          "If-Match": '"one"',
        },
        method: "PATCH",
      })
    ).status,
    204,
  )
  assert.equal(
    (
      await request(refreshedSession, {
        headers: { Authorization: "Bearer application-token" },
        method: "DELETE",
      })
    ).status,
    200,
  )
  assert.deepEqual(upstreamRequests, [
    {
      authorization: "Bearer application-token",
      method: "POST",
      url: "/whep",
    },
    {
      authorization: "Bearer application-token",
      method: "PATCH",
      url: "/whep/session",
    },
    {
      authorization: "Bearer application-token",
      method: "DELETE",
      url: "/whep/session",
    },
    {
      authorization: null,
      method: "POST",
      url: "/whep",
    },
    {
      authorization: null,
      method: "PATCH",
      url: "/whep/session",
    },
    {
      authorization: null,
      method: "DELETE",
      url: "/whep/session",
    },
    {
      authorization: "Bearer application-token",
      method: "POST",
      url: "/whep",
    },
    {
      authorization: "Bearer application-token",
      method: "PATCH",
      url: "/whep/session",
    },
    {
      authorization: "Bearer application-token",
      method: "DELETE",
      url: "/whep/session",
    },
  ])
  process.stdout.write(
    "rstream staging enforced edge scope and renewed an expired lifecycle credential.\n",
  )
} finally {
  await stopChild(forward)
  await closeServer(upstream)
}

async function validateTarget() {
  const contexts = await cliJSON(["context", "list", "--output", "json"])
  assert.ok(Array.isArray(contexts), "rstream context list is not an array")
  const selected = contexts.find((candidate) => candidate?.Name === context)
  assert.ok(selected, "RSTREAM_EDGE_AUTH_CONTEXT does not exist")
  assert.equal(
    canonicalEngine(String(selected.Engine ?? "")),
    canonicalEngine(expectedEngine),
    "selected context engine does not match RSTREAM_EDGE_AUTH_EXPECTED_ENGINE",
  )
  const response = await cliJSON([
    "--context",
    context,
    "project",
    "list",
    "--output",
    "json",
  ])
  assert.ok(
    Array.isArray(response?.projects),
    "rstream project list is invalid",
  )
  const project = response.projects.find(
    (candidate) => candidate?.id === projectID,
  )
  assert.ok(
    project,
    "RSTREAM_EDGE_AUTH_PROJECT_ID is not in the selected context",
  )
  assert.equal(
    String(project.endpoint ?? ""),
    String(selected.ProjectEndpoint ?? ""),
    "project endpoint does not match the selected context",
  )
}

async function delegatedToken(resources, permission, expiresInSeconds) {
  const arguments_ = [
    "--context",
    context,
    "token",
    "create",
    "--permission",
    permission,
    "--resources-json",
    JSON.stringify(resources),
    "--output",
    "json",
  ]
  if (expiresInSeconds !== undefined) {
    arguments_.push("--expires-in", String(expiresInSeconds))
  }
  const response = await cliJSON(arguments_)
  assert.equal(typeof response.token, "string")
  return response.token
}

function connectResources(tunnelID) {
  return {
    tunnels: {
      projects: [projectID],
      scopes: {
        tunnels: {
          connect: {
            filters: {
              id: tunnelID,
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
  }
}

async function waitUntilExpired(token) {
  const payload = JSON.parse(
    Buffer.from(token.split(".")[1], "base64url").toString("utf8"),
  )
  assert.equal(typeof payload.exp, "number")
  const remainingMilliseconds = payload.exp * 1000 - Date.now()
  if (remainingMilliseconds > 0) {
    await delay(remainingMilliseconds + 1_100)
  }
}

async function waitForTunnel(childFailure) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    const failure = await Promise.race([
      childFailure,
      delay(250).then(() => null),
    ])
    if (failure) {
      throw failure
    }
    const tunnels = await cliJSON([
      "--context",
      context,
      "tunnel",
      "list",
      "--filter",
      `name=${tunnelName},status=online`,
      "--output",
      "json",
    ])
    if (Array.isArray(tunnels) && tunnels.length > 0) {
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

async function cliJSON(arguments_) {
  const { stdout } = await execute(
    process.env.RSTREAM_BIN ?? "rstream",
    arguments_,
    {
      maxBuffer: 1024 * 1024,
    },
  )
  return JSON.parse(stdout)
}

function edgeURL(url, credential) {
  const result = new URL(url)
  result.searchParams.set("rstream.token", credential)
  return result
}

function request(url, init) {
  return fetch(url, {
    ...init,
    credentials: "omit",
    redirect: "manual",
    signal: AbortSignal.timeout(10_000),
  })
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
    delay(10_000).then(() => false),
  ])
  if (!exited) {
    child.kill("SIGKILL")
    await new Promise((resolve) => child.once("exit", resolve))
  }
}

function canonicalEngine(value) {
  const endpoint = new URL(value.includes("://") ? value : `https://${value}`)
  const port = endpoint.port || (endpoint.protocol === "https:" ? "443" : "80")
  return `${endpoint.hostname.toLowerCase()}:${port}`
}

function requiredEnvironment(name) {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value
}

function sanitize(value) {
  return value.replaceAll(/Bearer\s+\S+/gi, "Bearer [redacted]").trim()
}
