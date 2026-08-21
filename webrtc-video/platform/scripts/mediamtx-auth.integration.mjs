import assert from "node:assert/strict"
import { generateKeyPairSync } from "node:crypto"
import { access, mkdtemp, rm, writeFile } from "node:fs/promises"
import { createServer } from "node:http"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { spawn } from "node:child_process"

import { MediaMTXTokenService } from "../src/lib/video-distributor-token.ts"

const issuer = "rstream-webrtc-video-platform-integration"
const audience = "rstream-mediamtx-integration"
const deviceID = "fd8c2b34-1da2-4c71-8f38-343af59c0a11"
const path = `devices/${deviceID}`
const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 })
const tokens = new MediaMTXTokenService({
  audience,
  issuer,
  privateKeyBase64: privateKey
    .export({ format: "der", type: "pkcs8" })
    .toString("base64"),
})
const jwks = createServer((request, response) => {
  if (request.url !== "/jwks") {
    response.writeHead(404).end()
    return
  }
  response.setHeader("Content-Type", "application/json")
  response.end(JSON.stringify(tokens.jwks()))
})
await listen(jwks)
const jwksAddress = jwks.address()
assert(jwksAddress && typeof jwksAddress !== "string")
const httpPort = await availableTCPPort()
const icePort = await availableUDPPort()
const directory = await mkdtemp(join(tmpdir(), "rstream-mediamtx-auth-"))
const config = join(directory, "mediamtx.yml")
const onDemandMarker = join(directory, "on-demand-started")
const onDemandScript = join(directory, "on-demand.sh")
await writeFile(
  onDemandScript,
  `#!/bin/sh
set -eu
printf started > ${shellQuote(onDemandMarker)}
sleep 30
`,
  { mode: 0o700 },
)
await writeFile(
  config,
  `logLevel: info
logDestinations: [stdout]
authMethod: jwt
authJWTJWKS: http://127.0.0.1:${jwksAddress.port}/jwks
authJWTIssuer: ${issuer}
authJWTAudience: ${audience}
authJWTExclude: []
rtsp: false
rtmp: false
hls: false
srt: false
moq: false
playback: false
api: false
metrics: false
pprof: false
webrtc: true
webrtcAddress: 127.0.0.1:${httpPort}
webrtcLocalUDPAddress: 127.0.0.1:${icePort}
webrtcLocalTCPAddress: ""
webrtcIPsFromInterfaces: false
webrtcAdditionalHosts: [127.0.0.1]
paths:
  all_others:
    runOnDemand: ${onDemandScript}
    runOnDemandStartTimeout: 1s
    runOnDemandCloseAfter: 1s
`,
  { mode: 0o600 },
)
const logs = []
const mediaMTX = spawn(
  process.env.RSTREAM_MEDIAMTX_BINARY || "mediamtx",
  [config],
  {
    stdio: ["ignore", "pipe", "pipe"],
  },
)
mediaMTX.stdout.on("data", (chunk) => logs.push(String(chunk)))
mediaMTX.stderr.on("data", (chunk) => logs.push(String(chunk)))
try {
  await waitForHTTP(httpPort, mediaMTX)
  const read = tokens.sign({
    action: "read",
    path,
    subject: "viewer",
    ttlSeconds: 60,
  })
  const publish = tokens.sign({
    action: "publish",
    path,
    subject: "distributor",
    ttlSeconds: 60,
  })
  const expired = tokens.sign({
    action: "read",
    now: new Date(Date.now() - 120_000),
    path,
    subject: "expired-viewer",
    ttlSeconds: 1,
  })
  const base = `http://127.0.0.1:${httpPort}/${path}`
  assert.equal(await exchange(`${base}/whep`), 401)
  await assertFileRemainsMissing(onDemandMarker)
  assert.equal(await exchange(`${base}/whep`, expired), 401)
  await assertFileRemainsMissing(onDemandMarker)
  assert.equal(await exchange(`${base}/whep`, publish), 401)
  await assertFileRemainsMissing(onDemandMarker)
  const wrongPath = tokens.sign({
    action: "read",
    path: "devices/0a3c4d51-8d1f-4d59-a824-5cddcaa98f27",
    subject: "wrong-path-viewer",
    ttlSeconds: 60,
  })
  assert.equal(await exchange(`${base}/whep`, wrongPath), 401)
  await assertFileRemainsMissing(onDemandMarker)
  assert.equal(await exchange(`${base}/whep`, read), 400)
  await waitForFile(onDemandMarker)
  assert.equal(await exchange(`${base}/whip`, read, "sendonly"), 401)
  assert.equal(await exchange(`${base}/whip`, publish, "sendonly"), 201)
  const wrongPathPublish = tokens.sign({
    action: "publish",
    path: "devices/0a3c4d51-8d1f-4d59-a824-5cddcaa98f27",
    subject: "wrong-path-distributor",
    ttlSeconds: 60,
  })
  const expiredPublish = tokens.sign({
    action: "publish",
    now: new Date(Date.now() - 120_000),
    path,
    subject: "expired-distributor",
    ttlSeconds: 1,
  })
  const resourceAuthorization = {}
  for (const [name, token] of [
    ["missing", ""],
    ["expired", expiredPublish],
    ["wrongPath", wrongPathPublish],
    ["valid", publish],
  ]) {
    const session = await createSession(`${base}/whip`, publish, "sendonly")
    assert.equal(session.status, 201)
    assert(session.location)
    assertOpaqueSessionURL(session.location)
    resourceAuthorization[name] = {
      patch: await patchSession(session.location, token),
      delete: await deleteSession(session.location, token),
    }
  }
  assert.deepEqual(resourceAuthorization, {
    expired: { delete: 200, patch: 204 },
    missing: { delete: 200, patch: 204 },
    valid: { delete: 200, patch: 204 },
    wrongPath: { delete: 200, patch: 204 },
  })
  process.stdout.write(
    "MediaMTX authenticated session creation and bound lifecycle requests to opaque resource URLs.\n",
  )
} catch (error) {
  process.stderr.write(logs.join(""))
  throw error
} finally {
  mediaMTX.kill("SIGINT")
  await Promise.race([
    new Promise((resolve) => mediaMTX.once("exit", resolve)),
    new Promise((resolve) => setTimeout(resolve, 5000)).then(() => {
      if (mediaMTX.exitCode === null) {
        mediaMTX.kill("SIGKILL")
      }
    }),
  ])
  await new Promise((resolve) => jwks.close(resolve))
  await rm(directory, { force: true, recursive: true })
}

async function assertFileRemainsMissing(path) {
  await new Promise((resolve) => setTimeout(resolve, 100))
  await assert.rejects(() => access(path), { code: "ENOENT" })
}

async function waitForFile(path) {
  const deadline = Date.now() + 1000
  while (Date.now() < deadline) {
    try {
      await access(path)
      return
    } catch (error) {
      if (error?.code !== "ENOENT") {
        throw error
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  await access(path)
}

function shellQuote(value) {
  return `'${value.replaceAll("'", `'"'"'`)}'`
}

async function exchange(url, token = "", direction = "recvonly") {
  const session = await createSession(url, token, direction)
  if (session.status === 201 && session.location) {
    assert.equal(await deleteSession(session.location, token), 200)
  }
  return session.status
}

async function createSession(url, token = "", direction = "recvonly") {
  const headers = {
    Accept: "application/sdp",
    "Content-Type": "application/sdp",
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  const response = await fetch(url, {
    body: offer(direction),
    headers,
    method: "POST",
    redirect: "error",
    signal: AbortSignal.timeout(5000),
  })
  const location = response.headers.get("location")
  await response.body?.cancel()
  return {
    location: location ? new URL(location, url) : null,
    status: response.status,
  }
}

async function deleteSession(url, token) {
  const response = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    method: "DELETE",
    redirect: "error",
    signal: AbortSignal.timeout(5000),
  })
  await response.body?.cancel()
  return response.status
}

async function patchSession(url, token) {
  const response = await fetch(url, {
    body: [
      "a=group:BUNDLE 0",
      "m=video 9 UDP/TLS/RTP/SAVPF 96",
      "a=mid:0",
      "a=ice-ufrag:rstreamauth",
      "a=ice-pwd:rstream-auth-integration-test",
      "a=end-of-candidates",
      "",
    ].join("\r\n"),
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      "Content-Type": "application/trickle-ice-sdpfrag",
      "If-Match": "*",
    },
    method: "PATCH",
    redirect: "error",
    signal: AbortSignal.timeout(5000),
  })
  await response.body?.cancel()
  return response.status
}

function assertOpaqueSessionURL(url) {
  const identifier = url.pathname.split("/").at(-1)
  assert.match(
    identifier,
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  )
  assert.equal(url.searchParams.has("token"), false)
}

function offer(direction) {
  const source = direction === "sendonly"
  const fingerprint = Array.from({ length: 32 }, () => "AA").join(":")
  return [
    "v=0",
    "o=- 1 1 IN IP4 127.0.0.1",
    "s=-",
    "t=0 0",
    "a=group:BUNDLE 0",
    "a=msid-semantic: WMS rstream",
    "m=video 9 UDP/TLS/RTP/SAVPF 96",
    "c=IN IP4 0.0.0.0",
    "a=ice-ufrag:rstreamauth",
    "a=ice-pwd:rstream-auth-integration-test",
    `a=fingerprint:sha-256 ${fingerprint}`,
    "a=setup:actpass",
    "a=mid:0",
    `a=${direction}`,
    "a=rtcp-mux",
    "a=rtcp-mux-only",
    "a=rtpmap:96 H264/90000",
    "a=fmtp:96 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
    "a=msid:rstream video",
    ...(source
      ? ["a=ssrc:1234 cname:rstream", "a=ssrc:1234 msid:rstream video"]
      : []),
    "a=candidate:1 1 UDP 2130706431 127.0.0.1 50000 typ host",
    "a=end-of-candidates",
    "",
  ].join("\r\n")
}

async function waitForHTTP(port, child) {
  const deadline = Date.now() + 5000
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`MediaMTX exited with code ${child.exitCode}`)
    }
    try {
      const response = await fetch(`http://127.0.0.1:${port}/`, {
        signal: AbortSignal.timeout(500),
      })
      await response.body?.cancel()
      return
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 25))
    }
  }
  throw new Error("MediaMTX did not start within five seconds")
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
}

async function availableTCPPort() {
  const server = createServer()
  await listen(server)
  const address = server.address()
  assert(address && typeof address !== "string")
  await new Promise((resolve) => server.close(resolve))
  return address.port
}

async function availableUDPPort() {
  const { createSocket } = await import("node:dgram")
  const socket = createSocket("udp4")
  await new Promise((resolve, reject) => {
    socket.once("error", reject)
    socket.bind(0, "127.0.0.1", resolve)
  })
  const address = socket.address()
  await new Promise((resolve) => socket.close(resolve))
  return address.port
}
