import assert from "node:assert/strict"
import { spawn } from "node:child_process"
import { generateKeyPairSync } from "node:crypto"
import {
  access,
  mkdir,
  mkdtemp,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises"
import { createServer } from "node:http"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { setTimeout as delay } from "node:timers/promises"
import { MediaMTXPlayback } from "../src/lib/mediamtx-playback.ts"
import { MediaMTXTokenService } from "../src/lib/video-distributor-token.ts"

const directory = await mkdtemp(join(tmpdir(), "rstream-recording-"))
const device = "devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11"
const other = "devices/0a3c4d51-8d1f-4d59-a824-5cddcaa98f27"
const marker = join(directory, "on-demand-started")
const issuer = "recording-integration",
  audience = "recording-mediamtx"
const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 })
const tokens = new MediaMTXTokenService({
  issuer,
  audience,
  privateKeyBase64: privateKey
    .export({ format: "der", type: "pkcs8" })
    .toString("base64"),
})
const token = (action, path = device, now = new Date(), ttlSeconds = 60) =>
  tokens.sign({ action, path, subject: "integration", now, ttlSeconds })
let clipBytes
const jwks = createServer((request, response) => {
  if (request.url === "/jwks") {
    response.setHeader("Content-Type", "application/json")
    response.end(JSON.stringify(tokens.jwks()))
    return
  }
  if (request.url === "/clip" && clipBytes) {
    response.setHeader("Content-Type", "video/mp4")
    response.setHeader("Accept-Ranges", "none")
    response.end(clipBytes)
    return
  }
  response.writeHead(404).end()
})
let server, publisher
const logs = []
try {
  const jwksPort = await listen(jwks)
  const port = await unusedPort(),
    rtsp = await unusedPort()
  await writeFile(
    join(directory, "on-demand.sh"),
    `#!/bin/sh\nprintf started > ${quote(marker)}\nsleep 30\n`,
    { mode: 0o700 },
  )
  await writeFile(
    join(directory, "mediamtx.yml"),
    `logLevel: info
logDestinations: [stdout]
authMethod: jwt
authJWTJWKS: http://127.0.0.1:${jwksPort}/jwks
authJWTIssuer: ${issuer}
authJWTAudience: ${audience}
api: false
metrics: false
pprof: false
rtsp: true
rtspAddress: 127.0.0.1:${rtsp}
rtspTransports: [tcp]
rtmp: false
hls: false
srt: false
moq: false
webrtc: false
playback: true
playbackAddress: 127.0.0.1:${port}
playbackAllowOrigins: []
pathDefaults:
  record: true
  recordPath: ${JSON.stringify(join(directory, "recordings/%path/%Y-%m-%d_%H-%M-%S-%f"))}
  recordFormat: fmp4
  recordPartDuration: 1s
  recordMaxPartSize: 8M
  recordSegmentDuration: 2s
  recordDeleteAfter: 20s
  runOnDemand: ${JSON.stringify(join(directory, "on-demand.sh"))}
  runOnDemandStartTimeout: 1s
  runOnDemandCloseAfter: 1s
paths:
  all_others:
`,
    { mode: 0o600 },
  )
  server = spawnChild(process.env.RSTREAM_MEDIAMTX_BINARY || "mediamtx", [
    join(directory, "mediamtx.yml"),
  ])
  const endpoint = `http://127.0.0.1:${port}`
  const client = new MediaMTXPlayback({
    endpoint,
    windowSeconds: 30,
    credential: (path) => token("playback", path),
  })
  await until(
    async () => {
      try {
        const response = await fetch(`${endpoint}/list?path=${device}`, {
          signal: AbortSignal.timeout(500),
        })
        await response.body?.cancel()
        return response.status === 401
      } catch {
        return false
      }
    },
    5000,
    "playback listener",
  )
  for (const [name, value] of [
    ["missing", ""],
    ["read", token("read")],
    ["publish", token("publish")],
    ["wrong path", token("playback", other)],
    ["expired", token("playback", device, new Date(Date.now() - 120000), 1)],
  ]) {
    const response = await fetch(`${endpoint}/list?path=${device}`, {
      headers: value ? { Authorization: `Bearer ${value}` } : {},
      signal: AbortSignal.timeout(5000),
    })
    assert.equal(response.status, 401, name)
    await response.body?.cancel()
  }
  assert.deepEqual(
    (await client.index(device, AbortSignal.timeout(6000))).spans,
    [],
  )
  await assert.rejects(access(marker), { code: "ENOENT" })
  await mkdir(join(directory, "recordings", device), { recursive: true })
  const emptyDirectory = await fetch(`${endpoint}/list?path=${device}`, {
    headers: { Authorization: `Bearer ${token("playback")}` },
  })
  assert.equal(emptyDirectory.status, 404)
  assert.deepEqual(await emptyDirectory.json(), {
    status: "error",
    error: "no recording segments found",
  })
  publisher = spawnChild(process.env.RSTREAM_FFMPEG_BINARY || "ffmpeg", [
    "-hide_banner",
    "-loglevel",
    "error",
    "-re",
    "-f",
    "lavfi",
    "-i",
    "testsrc2=size=640x360:rate=30",
    "-an",
    "-c:v",
    "libx264",
    "-pix_fmt",
    "yuv420p",
    "-profile:v",
    "baseline",
    "-level:v",
    "3.1",
    "-preset",
    "ultrafast",
    "-tune",
    "zerolatency",
    "-g",
    "30",
    "-bf",
    "0",
    "-b:v",
    "1000k",
    "-t",
    "8",
    "-f",
    "rtsp",
    "-rtsp_transport",
    "tcp",
    `rtsp://127.0.0.1:${rtsp}/${device}?token=${token("publish")}`,
  ])
  await completion(publisher, 15000)
  const index = await client.index(device, AbortSignal.timeout(6000))
  assert.ok(index.spans.length > 0, "published source must have recorded spans")
  const span = index.spans.at(-1)
  const end = Date.parse(span.end),
    start = Math.max(Date.parse(span.start), end - 3000)
  const response = await client.clip(
    device,
    new URLSearchParams({
      start: new Date(start).toISOString(),
      duration: "1.5",
    }),
    AbortSignal.timeout(10000),
  )
  clipBytes = Buffer.from(await response.arrayBuffer())
  assert.ok(clipBytes.length > 1000, "MP4 body is not empty")
  const clipPath = join(directory, "clip.mp4")
  await writeFile(clipPath, clipBytes)
  const probe = await capture(process.env.RSTREAM_FFPROBE_BINARY || "ffprobe", [
    "-v",
    "error",
    "-show_entries",
    "stream=codec_name,width,height:format=duration",
    "-of",
    "json",
    clipPath,
  ])
  const decoded = JSON.parse(probe)
  assert.equal(decoded.streams[0].codec_name, "h264")
  assert.equal(decoded.streams[0].width, 640)
  assert.equal(decoded.streams[0].height, 360)
  assert.ok(
    Number(decoded.format.duration) >= 1 &&
      Number(decoded.format.duration) <= 3,
  )
  const { chromium, firefox, webkit } = await import("playwright-core")
  for (const [name, type] of Object.entries({ chromium, firefox, webkit })) {
    const browser = await type.launch({
      headless: true,
      ...(name === "chromium" && process.platform === "darwin"
        ? {
            executablePath:
              "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
          }
        : {}),
    })
    try {
      const page = await browser.newPage()
      await page.setContent(
        `<video muted playsinline autoplay src="http://127.0.0.1:${jwksPort}/clip"></video>`,
      )
      await page.waitForFunction(
        () => {
          const v = document.querySelector("video")
          return v.videoWidth === 640 && v.currentTime > 0.2
        },
        null,
        { timeout: 15000 },
      )
      console.log(
        `PASS: ${name} decodes the authenticated MP4 clip without byte ranges`,
      )
    } finally {
      await browser.close()
    }
  }
  await assert.rejects(access(marker), { code: "ENOENT" })
  await until(
    async () => {
      try {
        return (
          await readdir(join(directory, "recordings"), { recursive: true })
        ).every((name) => !name.endsWith(".mp4"))
      } catch (error) {
        if (error.code === "ENOENT") return true
        throw error
      }
    },
    35000,
    "record retention cleanup",
  )
  await delay(2100)
  assert.deepEqual(
    (await client.index(device, AbortSignal.timeout(6000))).spans,
    [],
  )
  console.log(
    "PASS: playback JWT scope, no demand-driven startup, real recording, MP4 decoding, and retention cleanup",
  )
} catch (error) {
  // Tokens are ephemeral, but diagnostic output still must not disclose them.
  console.error(
    logs
      .join("")
      .replace(/rtsp:\/\/[^@\s]+@/g, "rtsp://[redacted]@")
      .replace(/eyJ[A-Za-z0-9_.-]+/g, "[redacted]"),
  )
  throw error
} finally {
  await stop(publisher)
  await stop(server)
  jwks.closeAllConnections()
  await new Promise((resolve) => jwks.close(resolve))
  await rm(directory, { recursive: true, force: true })
}
function quote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`
}
function spawnChild(command, args) {
  const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] })
  child.stdout.on("data", (chunk) => logs.push(String(chunk)))
  child.stderr.on("data", (chunk) => logs.push(String(chunk)))
  child.on("error", (error) => logs.push(error.message))
  return child
}
function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", () => resolve(server.address().port))
  })
}
async function unusedPort() {
  const server = createServer()
  const port = await listen(server)
  await new Promise((resolve) => server.close(resolve))
  return port
}
async function until(check, timeout, label) {
  const end = Date.now() + timeout
  while (Date.now() < end) {
    if (await check()) return
    await delay(100)
  }
  throw new Error(`Timed out: ${label}`)
}
async function completion(child, timeout) {
  if (child.exitCode !== null) {
    assert.equal(child.exitCode, 0)
    return
  }
  let timer
  try {
    await Promise.race([
      new Promise((resolve, reject) => {
        child.once("error", reject)
        child.once("exit", (code, signal) =>
          code === 0
            ? resolve()
            : reject(new Error(`process ended with ${signal ?? code}`)),
        )
      }),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error("process deadline")), timeout)
      }),
    ])
  } finally {
    clearTimeout(timer)
  }
}
async function stop(child) {
  if (
    !child ||
    !child.pid ||
    child.exitCode !== null ||
    child.signalCode !== null
  )
    return
  child.kill("SIGINT")
  try {
    await completion(child, 3000)
  } catch {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill("SIGKILL")
      await new Promise((resolve) => child.once("exit", resolve))
    }
  }
}
async function capture(command, args) {
  const child = spawnChild(command, args)
  let result = ""
  child.stdout.on("data", (chunk) => (result += String(chunk)))
  try {
    await completion(child, 10000)
    return result
  } finally {
    await stop(child)
  }
}
