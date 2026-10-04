// Real rstream tunnel publication, Next.js routes and isolated PostgreSQL.
// Only GitHub membership is mocked; OAuth sign-in itself is not qualified here.
import assert from "node:assert/strict"
import { execFileSync, spawn, spawnSync } from "node:child_process"
import { randomUUID } from "node:crypto"
import {
  chmodSync,
  existsSync,
  readdirSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs"
import { createRequire } from "node:module"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { setTimeout as delay } from "node:timers/promises"
import { installPlaybackStartupTiming } from "./playback-startup.mjs"

const root = resolve(import.meta.dirname, "..")
assert.ok(
  process.argv[2],
  "Usage: live-discovery.integration.mjs OUTPUT_DIRECTORY",
)
const output = resolve(process.argv[2])
assert.ok(
  !existsSync(output) || readdirSync(output).length === 0,
  "Output directory must be empty",
)
const image = process.env.RSTREAM_DISCOVERY_PRODUCER_IMAGE
assert.ok(image, "RSTREAM_DISCOVERY_PRODUCER_IMAGE is required")
const distribution = process.env.RSTREAM_DISCOVERY_DISTRIBUTOR ?? "direct"
assert.ok(["direct", "mediamtx"].includes(distribution))
const distributed = distribution === "mediamtx"
assert.ok(
  !distributed || process.env.RSTREAM_DISCOVERY_BROWSER,
  "MediaMTX qualification requires RSTREAM_DISCOVERY_BROWSER",
)
const require = createRequire(join(root, "package.json"))
const {
  RstreamClient,
  getTunnelsProjectEngine,
} = require("@rstreamlabs/rstream")
const { RstreamTunnelsClient } = require("@rstreamlabs/tunnels")
const { Client } = require("pg")
process.loadEnvFile(join(root, ".env.local"))
mkdirSync(output, { recursive: true, mode: 0o700 })
const runtime = mkdtempSync(join(tmpdir(), "rstream-live-discovery-"))
const suffix = randomUUID()
const databaseName = `rstream-discovery-db-${suffix}`
const producerName = `rstream-discovery-producer-${suffix}`
const deviceID = randomUUID()
const tunnelName = `video-${deviceID}`
const sessions = {
  alice: randomUUID(),
  bob: randomUUID(),
  outsider: randomUUID(),
}
const gates = {}
let databaseStarted = false,
  producerStarted = false,
  child,
  db
let origin,
  baseEnvironment,
  nextLog = ""
let producerSequence = 0
let result
let stage = "setup"
let playback = null
let stackState = null
let stackSequence = 0
let childFailure = null
const command = (file, args, options = {}) =>
  execFileSync(file, args, {
    encoding: "utf8",
    timeout: 60000,
    stdio: ["pipe", "pipe", "pipe"],
    ...options,
  }).trim()
const docker = (...args) => command("docker", args)
const json = (name, value) =>
  writeFileSync(join(output, name), JSON.stringify(value, null, 2) + "\n", {
    mode: 0o600,
  })
const request = async (actor, path, body, method = body ? "POST" : "GET") => {
  const response = await fetch(origin + path, {
    method,
    redirect: "error",
    headers: {
      Cookie: `next-auth.session-token=${sessions[actor]}`,
      Origin: origin,
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.any([abort.signal, AbortSignal.timeout(12000)]),
  })
  return {
    status: response.status,
    body: response.status === 204 ? null : await response.json(),
  }
}
async function until(label, action, timeout = 45000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    abort.signal.throwIfAborted()
    if (await action()) return
    await delay(500)
  }
  throw new Error(`${label}: deadline exceeded`)
}
async function stopNext() {
  if (!child) return
  const active = child
  child = null
  if (stackState) {
    const logs = spawnSync("docker", ["logs", stackState.containerName], {
      encoding: "utf8",
      timeout: 10000,
      maxBuffer: 4 * 1024 * 1024,
    })
    if (logs.status === 0)
      writeFileSync(
        join(runtime, `mediamtx-${stackSequence}.log`),
        logs.stdout + logs.stderr,
        { mode: 0o600 },
      )
  }
  if (active.exitCode !== null || active.signalCode !== null) return
  const exited = new Promise((resolve) => active.once("exit", resolve))
  active.kill("SIGTERM")
  // The stack helper first stops MediaMTX, then its two owned process groups.
  // Give that bounded shutdown sequence time to complete before escalation.
  const timer = setTimeout(
    () => active.kill("SIGKILL"),
    distributed ? 45000 : 10000,
  )
  try {
    await exited
    assert.notEqual(
      active.signalCode,
      "SIGKILL",
      "Next.js stack shutdown timed out",
    )
    if (stackState) {
      assert.equal(
        docker(
          "ps",
          "--all",
          "--quiet",
          "--filter",
          `name=^/${stackState.containerName}$`,
        ),
        "",
        "MediaMTX container survived stack shutdown",
      )
      stackState = null
    }
  } finally {
    clearTimeout(timer)
  }
}
async function startNext(remember) {
  await stopNext()
  childFailure = null
  if (distributed) {
    stackSequence++
    const stateFile = join(runtime, `stack-${remember}.json`)
    origin = "http://localhost:3000"
    child = spawn(
      process.execPath,
      [
        join(root, "scripts/run-local-mediamtx.mjs"),
        "--exposure",
        "public",
        "--next-mode",
        "production",
        "--state-file",
        stateFile,
      ],
      {
        cwd: root,
        env: {
          ...baseEnvironment,
          NEXTAUTH_URL: origin,
          DEVICE_DISCOVERY_HISTORY_ENABLED: String(remember),
          // Next.js is started by the helper; only its GitHub HTTP calls are
          // substituted. The resolver, rstream and MediaMTX are real.
          NODE_OPTIONS: `${process.env.NODE_OPTIONS ?? ""} --import=${JSON.stringify(join(runtime, "github.mjs"))}`,
        },
        stdio: ["ignore", "pipe", "pipe"],
      },
    )
    child.once("error", (error) => {
      childFailure = error
    })
    for (const stream of [child.stdout, child.stderr])
      stream.on("data", (chunk) => {
        nextLog = (nextLog + chunk).slice(-64000)
      })
    await until(
      "MediaMTX stack readiness",
      async () => {
        if (childFailure) throw childFailure
        if (child.exitCode !== null || child.signalCode !== null)
          throw new Error("MediaMTX stack exited before readiness")
        if (!existsSync(stateFile)) return false
        stackState = JSON.parse(readFileSync(stateFile, "utf8"))
        return stackState.ready === true
      },
      240000,
    )
    return
  }
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const port = server.address().port
  await new Promise((resolve) => server.close(resolve))
  origin = `http://127.0.0.1:${port}`
  child = spawn(
    process.execPath,
    [
      "--import",
      join(runtime, "github.mjs"),
      join(root, "node_modules/next/dist/bin/next"),
      "start",
      "--hostname",
      "127.0.0.1",
      "--port",
      String(port),
    ],
    {
      cwd: root,
      env: {
        ...baseEnvironment,
        NEXTAUTH_URL: origin,
        DEVICE_DISCOVERY_HISTORY_ENABLED: String(remember),
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  )
  child.once("error", (error) => {
    childFailure = error
  })
  for (const stream of [child.stdout, child.stderr])
    stream.on("data", (chunk) => {
      nextLog = (nextLog + chunk).slice(-32000)
    })
  await until("Next.js readiness", async () => {
    if (childFailure) throw childFailure
    if (child.exitCode !== null)
      throw new Error("Next.js exited before readiness")
    try {
      return (await fetch(origin, { signal: AbortSignal.timeout(1000) })).ok
    } catch {
      return false
    }
  })
}
function stopProducer() {
  if (!producerStarted) return
  docker("stop", "--time", "10", producerName)
  const logs = spawnSync("docker", ["logs", producerName], {
    encoding: "utf8",
    timeout: 10000,
    maxBuffer: 4 * 1024 * 1024,
  })
  if (logs.error || logs.status !== 0)
    throw new Error("Could not collect producer logs")
  const log = logs.stdout + logs.stderr
  // Keep raw logs in the private runtime until sanitized in final cleanup.
  writeFileSync(join(runtime, `producer-${producerSequence}.log`), log, {
    mode: 0o600,
  })
  docker("rm", producerName)
  producerStarted = false
}
function startProducer(name) {
  abort.signal.throwIfAborted()
  assert.equal(producerStarted, false)
  producerSequence++
  docker(
    "run",
    "--detach",
    "--name",
    producerName,
    "--user",
    `${process.getuid()}:${process.getgid()}`,
    "--read-only",
    "--security-opt",
    "no-new-privileges",
    "--tmpfs",
    "/tmp:rw,noexec,nosuid,size=64m",
    "--env",
    "HOME=/tmp",
    "--env",
    "RSTREAM_CONFIG=/runtime/context.yaml",
    "--env",
    "RSTREAM_CONTEXT=discovery-qualification",
    "--env",
    `VIDEO_DEVICE_ID=${deviceID}`,
    "--env",
    `VIDEO_DEVICE_NAME=${name}`,
    "--mount",
    `type=bind,source=${join(runtime, "context.yaml")},target=/runtime/context.yaml,readonly`,
    "--mount",
    `type=bind,source=${join(root, "../producer/config.discovery.h264.yaml")},target=/runtime/producer.yaml,readonly`,
    image,
    "-config",
    "/runtime/producer.yaml",
  )
  producerStarted = true
}
async function device(actor = "alice") {
  const result = await request(actor, "/api/devices")
  assert.equal(result.status, 200, "inventory request must succeed")
  return result.body.devices.find((value) => value.id === deviceID)
}
async function qualifyPlayback() {
  const executablePath = process.env.RSTREAM_DISCOVERY_BROWSER
  if (!executablePath) return null
  const { chromium } = require("playwright-core")
  const browser = await chromium.launch({ executablePath, headless: true })
  const stopOnAbort = () => {
    void browser.close().catch(() => {})
  }
  abort.signal.addEventListener("abort", stopOnAbort, { once: true })
  try {
    abort.signal.throwIfAborted()
    const viewers = []
    let pageErrors = 0
    for (const actor of distributed ? ["alice", "bob"] : ["bob"]) {
      const context = await browser.newContext()
      await context.addCookies([
        {
          name: "next-auth.session-token",
          value: sessions[actor],
          url: origin,
        },
      ])
      await context.addInitScript(installPlaybackStartupTiming)
      const page = await context.newPage()
      page.on("pageerror", () => pageErrors++)
      let startup = null
      try {
        await page.goto(origin, {
          waitUntil: "domcontentloaded",
          timeout: 30000,
        })
        if (distributed)
          await page
            .getByText("Distribution path: MediaMTX", { exact: true })
            .waitFor({ timeout: 45000 })
        await page.waitForFunction(
          () => {
            const video = document.querySelector(
              ".video-player-picture > video",
            )
            return (
              video?.readyState >= 2 &&
              video.videoWidth === 1280 &&
              video.videoHeight === 720 &&
              window.__playbackStartup.snapshot().firstFrame !== null
            )
          },
          undefined,
          { timeout: 45000 },
        )
      } finally {
        // Preserve failed/missing evidence too, without hiding the original failure.
        startup = await page
          .evaluate(() => window.__playbackStartup?.snapshot())
          .catch(() => null)
        json(
          `playback-startup-${actor}.json`,
          startup ?? { measurementValid: false },
        )
      }
      viewers.push({ actor, page, context, startup })
    }
    const sample = (page) =>
      page.evaluate(async () => {
        const peer = window.__discoveryPeers.find(
          (value) => value.connectionState === "connected",
        )
        if (!peer) throw new Error("No connected discovery viewer")
        const reports = [...(await peer.getStats()).values()]
        const inbound = reports.find(
          (value) => value.type === "inbound-rtp" && value.kind === "video",
        )
        const transport = reports.find(
          (value) =>
            value.type === "transport" && value.selectedCandidatePairId,
        )
        const pair = reports.find(
          (value) => value.id === transport?.selectedCandidatePairId,
        )
        const local = reports.find(
          (value) => value.id === pair?.localCandidateId,
        )
        const remote = reports.find(
          (value) => value.id === pair?.remoteCandidateId,
        )
        return {
          at: performance.now(),
          framesDecoded: inbound?.framesDecoded ?? 0,
          width: inbound?.frameWidth,
          height: inbound?.frameHeight,
          localCandidateType: local?.candidateType,
          remoteCandidateType: remote?.candidateType,
        }
      })
    const measure = async (active) => {
      const before = await Promise.all(active.map(({ page }) => sample(page)))
      await delay(3000, undefined, { signal: abort.signal })
      const after = await Promise.all(active.map(({ page }) => sample(page)))
      return after.map((value, index) => {
        const framesPerSecond =
          ((value.framesDecoded - before[index].framesDecoded) * 1000) /
          (value.at - before[index].at)
        assert.ok(
          framesPerSecond >= 24,
          "Each discovered viewer must sustain at least 24 decoded fps",
        )
        assert.equal(value.width, 1280)
        assert.equal(value.height, 720)
        return { actor: active[index].actor, ...value, framesPerSecond }
      })
    }
    const observation = {
      distribution,
      startup: viewers.map(({ actor, startup }) => ({ actor, ...startup })),
      startupScope:
        "Authenticated dashboard navigation; membership and inventory caches already warm; producer process ready; source selected low; OAuth excluded",
      viewers: await measure(viewers),
    }
    const quality = await request("alice", `/api/devices/${deviceID}/quality`)
    assert.equal(quality.status, 200)
    assert.equal(quality.body.activeEncoders, 1)
    observation.activeEncoders = quality.body.activeEncoders
    if (distributed) {
      const readers = async (expected) => {
        await until(`${expected} MediaMTX readers`, async () => {
          const metrics = await request(
            "bob",
            `/api/devices/${deviceID}/metrics`,
          )
          return (
            metrics.status === 200 &&
            metrics.body.state === "ready" &&
            metrics.body.readers === expected
          )
        })
      }
      await readers(2)
      const select = viewers[0].page.getByLabel("Source quality", {
        exact: true,
      })
      await select.selectOption("medium")
      await until(
        "shared quality across separate member sessions",
        async () => {
          const state = await request("bob", `/api/devices/${deviceID}/quality`)
          return (
            state.status === 200 &&
            state.body.selected === "medium" &&
            state.body.activeEncoders === 1 &&
            (await viewers[1].page
              .getByLabel("Source quality", { exact: true })
              .inputValue()) === "medium"
          )
        },
      )
      observation.afterSharedQualityChange = await measure(viewers)
      assert.equal(
        (await request("outsider", `/api/devices/${deviceID}/viewer`, {}))
          .status,
        403,
      )
      assert.equal(
        (await request("outsider", `/api/devices/${deviceID}/metrics`)).status,
        403,
      )
      await viewers[0].context.close()
      await readers(1)
      observation.remainingMember = await measure(viewers.slice(1))
      assert.equal(
        (await request("bob", `/api/devices/${deviceID}/quality`)).body
          .activeEncoders,
        1,
      )
      // maxViewers=1 in the source profile makes a second upstream session
      // impossible; two measured MediaMTX readers therefore share one uplink.
      observation.mediaMTXReaders = 2
      observation.sourceViewerLimit = 1
      observation.sharedQuality = "medium"
      observation.continuesAfterFirstMemberCloses = true
    }
    assert.equal(pageErrors, 0)
    assert.ok(
      viewers.every(({ startup }) => startup?.measurementValid),
      "First presentation must be measured from navigation and authorization",
    )
    return observation
  } finally {
    abort.signal.removeEventListener("abort", stopOnAbort)
    await browser.close()
  }
}
const safetyTimer = setTimeout(
  () => process.emit("SIGTERM"),
  (distributed ? 15 : 8) * 60 * 1000,
)
const abort = new AbortController()
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => abort.abort(new Error("Qualification interrupted")))
try {
  const apiUrl = process.env.RSTREAM_API_URL ?? "https://rstream.io"
  const credentials = {
    clientId: process.env.RSTREAM_CLIENT_ID,
    clientSecret: process.env.RSTREAM_CLIENT_SECRET,
  }
  const controlPlane = new RstreamClient({
    apiUrl,
    credentials,
    fetch: (input, init) =>
      fetch(input, {
        ...init,
        signal: AbortSignal.any([abort.signal, AbortSignal.timeout(10000)]),
      }),
  })
  stage = "resolve project"
  const project = await controlPlane.tunnels.projects.resolveByEndpoint(
    process.env.RSTREAM_PROJECT_ENDPOINT,
  )
  assert.ok(
    !process.env.RSTREAM_PROJECT_ID ||
      process.env.RSTREAM_PROJECT_ID === project.id,
    "Configured project ID and endpoint must identify the same project",
  )
  const engine = getTunnelsProjectEngine(project)
  const client = new RstreamTunnelsClient({
    apiUrl,
    credentials,
    projectId: project.id,
    engine,
  })
  stage = "issue scoped producer credentials"
  const issued = await client.auth.createAuthToken({
    expires_in: 900,
    resources: {
      tunnels: {
        projects: [project.id],
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
  stage = "create private CLI context"
  command(
    "rstream",
    [
      "--config",
      join(runtime, "context.yaml"),
      "--api-url",
      apiUrl,
      "context",
      "create",
      "discovery-qualification",
      "--default",
      "--engine",
      engine,
      "--project-endpoint",
      process.env.RSTREAM_PROJECT_ENDPOINT,
      "--token-stdin",
      "--token-storage",
      "inline",
    ],
    { input: issued.token },
  )
  chmodSync(join(runtime, "context.yaml"), 0o600)
  stage = "start isolated database"
  docker(
    "run",
    "--detach",
    "--name",
    databaseName,
    "--env",
    "POSTGRES_PASSWORD=qualification",
    "--publish",
    "127.0.0.1::5432",
    "postgres:17-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193",
  )
  databaseStarted = true
  await until("PostgreSQL readiness", async () => {
    try {
      // Wait for the final TCP listener, not the temporary init server.
      docker(
        "exec",
        databaseName,
        "pg_isready",
        "-h",
        "127.0.0.1",
        "-U",
        "postgres",
      )
      return true
    } catch {
      return false
    }
  })
  const dbURL = `postgresql://postgres:qualification@${docker("port", databaseName, "5432/tcp")}/postgres`
  baseEnvironment = {
    ...process.env,
    POSTGRES_PRISMA_POOL_URL: dbURL,
    POSTGRES_PRISMA_DIRECT_URL: dbURL,
    NEXTAUTH_SECRET: randomUUID(),
    GITHUB_CLIENT_ID: "qualification",
    GITHUB_CLIENT_SECRET: "qualification",
    GITHUB_ORGANIZATION: "qualification-org",
    DEMO_CLEANUP_ENABLED: "false",
    DEVICE_ACCESS_MODE: "organization",
    DEVICE_INVENTORY_MODE: "discovered",
    RSTREAM_PROJECT_ID: project.id,
    RSTREAM_ENGINE: engine,
    VIDEO_DISTRIBUTOR: "direct",
    MEDIAMTX_METRICS_URL: "",
    MEDIAMTX_PLAYBACK_URL: "",
    NEXT_TELEMETRY_DISABLED: "1",
  }
  command(join(root, "node_modules/.bin/prisma"), ["migrate", "deploy"], {
    cwd: root,
    env: baseEnvironment,
  })
  db = new Client({
    connectionString: dbURL,
    connectionTimeoutMillis: 5000,
    query_timeout: 10000,
  })
  await db.connect()
  for (const [index, [actor, token]] of Object.entries(sessions).entries()) {
    await db.query(
      'INSERT INTO users(id,name,"updatedAt") VALUES($1,$1,now())',
      [actor],
    )
    await db.query(
      'INSERT INTO accounts("userId",type,provider,"providerAccountId",access_token,"updatedAt") VALUES($1,\'oauth\',\'github\',$2,$3,now())',
      [actor, String(index + 7), `member-${actor}`],
    )
    await db.query(
      'INSERT INTO sessions("sessionToken","userId",expires,"updatedAt") VALUES($1,$2,now()+interval \'1 hour\',now())',
      [token, actor],
    )
  }
  writeFileSync(
    join(runtime, "github.mjs"),
    `const nativeFetch=globalThis.fetch.bind(globalThis);globalThis.fetch=async(input,init)=>{const request=new Request(input,init);const url=new URL(request.url);if(url.hostname==='api.github.com'){if(url.pathname!=='/user/memberships/orgs/qualification-org')throw new Error('Unexpected GitHub route');const actor=request.headers.get('authorization')?.split('member-')[1];return Response.json({state:actor==='outsider'?'pending':'active',organization:{id:42,login:'qualification-org'},user:{id:actor==='alice'?7:actor==='bob'?8:9}});}return nativeFetch(input,init);};`,
    { mode: 0o600 },
  )
  stage = "start production Next.js"
  await startNext(true)
  assert.equal(await device(), undefined)
  stage = "publish and discover producer"
  startProducer("Discovery source")
  await until(
    "published source discovery",
    async () => (await device())?.online === true,
  )
  let first = await device()
  assert.equal(first.name, "Discovery source")
  assert.equal(first.inventory, "discovered")
  assert.equal(first.secretPrefix, null)
  assert.equal((await device("bob")).id, deviceID)
  assert.equal((await request("outsider", "/api/devices")).status, 403)
  assert.equal(
    (await request("bob", "/api/devices", { name: "Cannot enroll" })).status,
    409,
  )
  assert.equal(
    (await request("bob", `/api/devices/${deviceID}`, undefined, "DELETE"))
      .status,
    409,
  )
  assert.equal(
    (await db.query("SELECT count(*)::int AS count FROM devices")).rows[0]
      .count,
    0,
  )
  gates.realDiscoveryWithoutEnrollment = true
  stage = "source quality control"
  const quality = await request("alice", `/api/devices/${deviceID}/quality`)
  assert.equal(quality.status, 200)
  assert.deepEqual(
    quality.body.modes.map((mode) => mode.id),
    ["auto", "low", "medium", "high"],
  )
  assert.equal(
    quality.body.activeEncoders,
    0,
    "inventory and quality reads must not start media",
  )
  const changed = await request(
    "bob",
    `/api/devices/${deviceID}/quality`,
    { mode: "low", version: quality.body.version },
    "PUT",
  )
  assert.equal(changed.status, 200)
  assert.equal(changed.body.selected, "low")
  assert.equal(
    (
      await request(
        "alice",
        `/api/devices/${deviceID}/quality`,
        { mode: "high", version: quality.body.version },
        "PUT",
      )
    ).status,
    409,
  )
  assert.equal(
    (await request("alice", `/api/devices/${deviceID}/quality`)).body.selected,
    "low",
  )
  gates.sourceControlAndConcurrentSelection = true
  stage = `${distribution} playback from discovered producer`
  playback = await qualifyPlayback()
  if (playback) {
    const closedAt = performance.now()
    await until("encoder stops after viewer closure", async () => {
      const quality = await request("alice", `/api/devices/${deviceID}/quality`)
      return quality.status === 200 && quality.body.activeEncoders === 0
    })
    playback.encoderStoppedAfterBrowserClosureMilliseconds =
      performance.now() - closedAt
    gates[
      distributed
        ? "sharedMediaMTXPlaybackAndEncoderLifecycle"
        : "directPlaybackAndEncoderLifecycle"
    ] = true
  }
  stopProducer()
  await until("offline history", async () => (await device())?.online === false)
  assert.equal(
    (await request("bob", `/api/devices/${deviceID}/quality`)).status,
    404,
  )
  assert.equal(
    (await request("bob", `/api/devices/${deviceID}/viewer`, {})).status,
    404,
  )
  gates.historyDoesNotGrantSourceAccess = true
  stage = "reconnect and rename"
  startProducer("Renamed source")
  await until("renamed reconnection", async () => {
    const current = await device()
    return current?.online && current.name === "Renamed source"
  })
  const history = await db.query(
    'SELECT count(*)::int AS count FROM discovered_devices WHERE "deviceId"=$1',
    [deviceID],
  )
  assert.equal(history.rows[0].count, 1)
  gates.stableIdentityAndRename = true
  stopProducer()
  await until(
    "second offline history",
    async () => (await device())?.online === false,
  )
  stage = "live-only inventory"
  await startNext(false)
  assert.equal(await device(), undefined)
  await db.query('DELETE FROM discovered_devices WHERE "deviceId"=$1', [
    deviceID,
  ])
  startProducer("Live-only source")
  await until("live-only source", async () => (await device())?.online === true)
  assert.equal(
    (
      await db.query(
        'SELECT count(*)::int AS count FROM discovered_devices WHERE "deviceId"=$1',
        [deviceID],
      )
    ).rows[0].count,
    0,
  )
  stopProducer()
  await until("live-only removal", async () => (await device()) === undefined)
  gates.liveOnlyWithoutHistoryWrites = true
  abort.signal.throwIfAborted()
  result = {
    passed: true,
    gates,
    projectId: project.id,
    apiUrl,
    revision: command("git", ["rev-parse", "HEAD"], { cwd: root }),
    workingTreeDirty:
      command("git", ["status", "--porcelain"], { cwd: root }) !== "",
    image: docker("image", "inspect", "--format", "{{.Id}}", image),
    distribution,
    distributorImage: distributed
      ? docker(
          "image",
          "inspect",
          "--format",
          "{{.Id}}",
          "rstream-video-distributor:local",
        )
      : null,
    githubMembership: "fixture",
    mediaPlayback: playback ?? "not exercised",
    nextBuild: readFileSync(join(root, ".next/BUILD_ID"), "utf8").trim(),
  }
} catch (error) {
  // Do not serialize upstream exceptions or command buffers containing credentials.
  json("failure.json", {
    passed: false,
    gates,
    stage,
    name: error.name,
    commandStatus: Number.isInteger(error.status) ? error.status : null,
    message:
      error instanceof assert.AssertionError
        ? error.message
        : "Live discovery qualification failed; inspect sanitized runtime logs.",
  })
  process.exitCode = 1
} finally {
  clearTimeout(safetyTimer)
  try {
    stopProducer()
  } catch {
    process.exitCode = 1
    try {
      docker("rm", "--force", producerName)
    } catch {}
  }
  await stopNext().catch(() => {
    process.exitCode = 1
  })
  if (db)
    await db.end().catch(() => {
      process.exitCode = 1
    })
  if (databaseStarted) {
    try {
      docker("rm", "--force", databaseName)
    } catch {
      process.exitCode = 1
    }
  }
  const sanitize = join(
    root,
    "../distributor/qualification/end-to-end/sanitize-stream.mjs",
  )
  try {
    writeFileSync(
      join(output, "next.log"),
      command(process.execPath, [sanitize], { input: nextLog }),
      { mode: 0o600 },
    )
  } catch {
    process.exitCode = 1
  }
  for (let i = 1; i <= producerSequence; i++) {
    try {
      writeFileSync(
        join(output, `producer-${i}.log`),
        command(process.execPath, [sanitize], {
          input: readFileSync(join(runtime, `producer-${i}.log`), "utf8"),
        }),
        { mode: 0o600 },
      )
    } catch {
      process.exitCode = 1
    }
  }
  for (let i = 1; i <= stackSequence; i++) {
    const log = join(runtime, `mediamtx-${i}.log`)
    if (!existsSync(log)) continue
    try {
      writeFileSync(
        join(output, `mediamtx-${i}.log`),
        command(process.execPath, [sanitize], {
          input: readFileSync(log, "utf8"),
        }),
        { mode: 0o600 },
      )
    } catch {
      process.exitCode = 1
    }
  }
  rmSync(runtime, { recursive: true, force: true })
  if (result) {
    result.passed = !process.exitCode
    result.gates.cleanup = !process.exitCode
    json("result.json", result)
    if (result.passed)
      console.log(
        "PASS: live discovery, shared source controls, reconnect/rename, offline and live-only inventory",
      )
  }
}
