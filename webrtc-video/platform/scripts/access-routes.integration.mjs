// Real Next.js routes and PostgreSQL; only upstream GitHub/engine HTTP is mocked.
import assert from "node:assert/strict"
import { execFileSync, spawn } from "node:child_process"
import {
  createPrivateKey,
  generateKeyPairSync,
  randomUUID,
  randomBytes,
  sign,
} from "node:crypto"
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  writeFileSync,
  rmSync,
} from "node:fs"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { setTimeout as delay } from "node:timers/promises"
import { generateMediaMTXKeys } from "./generate-mediamtx-key.mjs"
import { Client } from "pg"
import { qualifyFullPage } from "./full-page.browser.mjs"
import { launchBrowserContext } from "./browser-context.mjs"

const root = resolve(import.meta.dirname, "..")
const configuredPort = process.env.RSTREAM_ACCESS_TEST_PORT
assert.ok(
  configuredPort === undefined ||
    (/^[1-9][0-9]{0,4}$/.test(configuredPort) &&
      Number(configuredPort) <= 65535),
  "RSTREAM_ACCESS_TEST_PORT must be a TCP port from 1 through 65535",
)
const runtime = mkdtempSync(join(tmpdir(), "rstream-video-access-"))
const name = `rstream-video-access-${randomUUID()}`
const docker = (...args) =>
  execFileSync("docker", args, {
    encoding: "utf8",
    timeout: 60000,
    stdio: ["pipe", "pipe", "pipe"],
  }).trim()
const sessions = {
  alice: randomUUID(),
  bob: randomUUID(),
  outsider: randomUUID(),
}
let started = false,
  child,
  output = "",
  baseEnvironment
const privateKey = generateKeyPairSync("ec", { namedCurve: "secp521r1" })
  .privateKey.export({ type: "pkcs8", format: "der" })
  .toString("hex")
const turnPublicKey = generateKeyPairSync("ec", { namedCurve: "secp521r1" })
  .publicKey.export({ type: "spki", format: "der" })
  .toString("hex")
async function stop() {
  if (!child) return
  const process = child
  child = null
  if (process.exitCode !== null || process.signalCode !== null) return
  const exit = new Promise((resolve) => process.once("exit", resolve))
  process.kill("SIGTERM")
  const timeout = setTimeout(() => process.kill("SIGKILL"), 10000)
  try {
    await exit
  } finally {
    clearTimeout(timeout)
  }
}
async function unusedPort() {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(Number(configuredPort ?? 0), "127.0.0.1", resolve)
  })
  const port = server.address().port
  await new Promise((resolve) => server.close(resolve))
  return port
}
async function start(mode) {
  await stop()
  output = ""
  const port = await unusedPort(),
    origin = `http://127.0.0.1:${port}`
  child = spawn(
    process.execPath,
    [
      "--import",
      join(runtime, "upstreams.mjs"),
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
        DEVICE_ACCESS_MODE: mode,
        NEXTAUTH_URL: origin,
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  )
  for (const stream of [child.stdout, child.stderr])
    stream.on("data", (chunk) => {
      output = (output + chunk).slice(-12000)
    })
  for (let i = 0; i < 100; i++) {
    if (child.exitCode !== null) throw new Error(`Next.js exited: ${output}`)
    try {
      if ((await fetch(origin, { signal: AbortSignal.timeout(1000) })).ok)
        return origin
    } catch {}
    await delay(100)
  }
  throw new Error(`Next.js readiness deadline: ${output}`)
}
async function request(
  origin,
  actor,
  path,
  body,
  method = body ? "POST" : "GET",
  crossOrigin = false,
) {
  const response = await fetch(`${origin}${path}`, {
    method,
    headers: {
      Cookie: `next-auth.session-token=${sessions[actor]}`,
      Origin: crossOrigin ? "https://attacker.example" : origin,
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(10000),
  })
  return {
    status: response.status,
    body: response.status === 204 ? null : await response.json(),
  }
}
try {
  docker(
    "run",
    "--rm",
    "-d",
    "--name",
    name,
    "-e",
    "POSTGRES_PASSWORD=qualification",
    "-p",
    "127.0.0.1::5432",
    "postgres:17-alpine",
  )
  started = true
  let ready = false
  for (let i = 0; i < 100; i++) {
    try {
      // Wait for the final TCP listener, not the temporary init server.
      docker("exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres")
      ready = true
      break
    } catch {
      await delay(100)
    }
  }
  assert.ok(ready)
  const db = `postgresql://postgres:qualification@${docker("port", name, "5432/tcp")}/postgres`
  baseEnvironment = {
    ...process.env,
    POSTGRES_PRISMA_POOL_URL: db,
    POSTGRES_PRISMA_DIRECT_URL: db,
    NEXTAUTH_SECRET: randomUUID(),
    GITHUB_CLIENT_ID: "qualification",
    GITHUB_CLIENT_SECRET: "qualification",
    GITHUB_ORGANIZATION: "acme",
    DEMO_CLEANUP_ENABLED: "false",
    DEVICE_INVENTORY_MODE: "managed",
    DEVICE_DISCOVERY_HISTORY_ENABLED: "true",
    RSTREAM_CLIENT_ID: "qualification",
    RSTREAM_API_URL: "https://control.qualification.invalid",
    RSTREAM_TURN_KEYRING_BASE_URL: "https://control.qualification.invalid",
    RSTREAM_CLIENT_SECRET: privateKey,
    RSTREAM_PROJECT_ENDPOINT: "qualification-endpoint",
    RSTREAM_PROJECT_ID: "qualification-project",
    RSTREAM_ENGINE: "engine.qualification.invalid:443",
    VIDEO_DISTRIBUTOR: "direct",
    MEDIAMTX_METRICS_URL: "",
    MEDIAMTX_PLAYBACK_URL: "",
    NEXT_TELEMETRY_DISABLED: "1",
  }
  execFileSync(join(root, "node_modules/.bin/prisma"), ["migrate", "deploy"], {
    cwd: root,
    env: baseEnvironment,
    timeout: 60000,
    stdio: "pipe",
  })
  const sql = Object.entries(sessions)
    .map(
      (
        [user, token],
        i,
      ) => `INSERT INTO users(id,name,"updatedAt") VALUES('${user}','${user}',now());
 INSERT INTO accounts("userId",type,provider,"providerAccountId",access_token,"updatedAt") VALUES('${user}','oauth','github','${i + 7}','member-${user}',now());
 INSERT INTO sessions("sessionToken","userId",expires,"updatedAt") VALUES('${token}','${user}',now()+interval '1 hour',now());`,
    )
    .join("\n")
  execFileSync(
    "docker",
    ["exec", "-i", name, "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1"],
    { input: sql, stdio: ["pipe", "pipe", "pipe"] },
  )
  const inventoryPath = join(runtime, "inventory.json")
  const callsPath = join(runtime, "upstream-calls.log")
  const resetCalls = () => writeFileSync(callsPath, "")
  const upstreamCalls = () =>
    readFileSync(callsPath, "utf8").trim().split("\n").filter(Boolean).sort()
  resetCalls()
  const setInventory = (value) =>
    writeFileSync(inventoryPath, JSON.stringify(value))
  setInventory([])
  writeFileSync(
    join(runtime, "upstreams.mjs"),
    `import { readFileSync, appendFileSync } from 'node:fs';
 const inventoryPath=${JSON.stringify(inventoryPath)};
 const callsPath=${JSON.stringify(callsPath)};
 const count=kind=>appendFileSync(callsPath,kind+'\\n');
 const original=globalThis.fetch.bind(globalThis);globalThis.fetch=async(input,init)=>{
 const request=new Request(input,init),url=new URL(request.url);
 if(url.hostname==='api.github.com'){
  const actor=request.headers.get('authorization')?.split('member-')[1];
  return Response.json({state:actor==='outsider'?'pending':'active',organization:{id:42,login:'acme'},user:{id:actor==='alice'?7:actor==='bob'?8:9}});
 }
 if(url.hostname==='engine.qualification.invalid'){
  const filters=JSON.parse(url.searchParams.get('params')??'{}').filters;
  const distributor=filters?.name==='qualification-media';
  count(distributor?'distribution':'inventory');
  const inventory=JSON.parse(readFileSync(inventoryPath,'utf8'));
  if(inventory.error)throw new Error('simulated engine outage');
  if(distributor)return Response.json(inventory.length?[{id:'distribution-tunnel',client_id:'qualification-distributor',project_id:'qualification-project',name:'qualification-media',host:'distribution.qualification.invalid',status:'online',protocol:'http',publish:true,token_auth:true}]:[]);
  return Response.json(inventory);
 }
 if(url.hostname==='control.qualification.invalid'){
  if(url.pathname==='/api/projects/tunnels/resolve/qualification-endpoint'){
   count('project');
   return Response.json({id:'qualification-project',workspaceId:'qualification',name:'Qualification',endpoint:'qualification-endpoint',url:'engine.qualification.invalid',domain:'engine.qualification.invalid',enginePort:443,turnDomain:'turn.qualification.invalid',turnRealm:'qualification',turnPort:3478,turnsPort:5349,status:'active',routing:'regional',provider:'other',plan:'pro',deployment:'shared'});
  }
  if(url.pathname==='/keyrings/turn/qualification.spki.der.hex'){
   count('keyring');
   return new Response(${JSON.stringify(turnPublicKey)});
  }
  throw new Error('Unexpected control-plane route');
 }
 if(url.hostname==='playback.qualification.invalid'){
  const token=request.headers.get('authorization')?.replace(/^Bearer /,'');
  const claims=JSON.parse(Buffer.from(token.split('.')[1],'base64url').toString());
  const permission=claims.mediamtx_permissions;
  if(permission.length!==1||permission[0].action!=='playback'||permission[0].path!==url.searchParams.get('path'))throw new Error('incorrect recording credential scope');
  if(url.pathname==='/list')return Response.json([{start:new Date(Date.now()-60000).toISOString(),duration:30,url:'http://private.invalid/get?secret=never-forward'}]);
  if(url.pathname==='/get'&&Number(url.searchParams.get('duration'))<=30)return new Response('test-media',{headers:{'Content-Type':'video/mp4'}});
  throw new Error('invalid recording request');
 }
 if(url.hostname==='metrics.qualification.invalid'){
  if(url.searchParams.get('type')!=='paths')throw new Error('Unscoped metrics scrape');
  const path=url.searchParams.get('path');
  const labels='name="'+path+'",state="ready"';
  return new Response('paths{'+labels+'} 1\\npaths_readers{'+labels+',readerType="webRTCSession"} 2\\npaths_inbound_bytes{'+labels+'} 1000\\npaths_outbound_bytes{'+labels+'} 2000\\n', {headers:{'Content-Type':'text/plain'}});
 }
 if(url.hostname==='video.qualification.invalid'){
  const claims=JSON.stringify(JSON.parse(Buffer.from(url.searchParams.get('rstream.token').split('.')[1],'base64url').toString()));
  if(!claims.includes('^/api/quality$')||!claims.includes('"inventory":"discovered"'))throw new Error('incorrect source control credential scope');
  return Response.json({modes:[{id:'auto',label:'Auto',bitrateKbps:0},{id:'low',label:'Low',bitrateKbps:1000}],selected:'auto',version:'0123456789abcdef0123456789abcdef:1',activeEncoders:1,minAppliedBitrateKbps:1000,maxAppliedBitrateKbps:1000,failedUpdates:0});
 }
 if(url.hostname==='127.0.0.1'||url.hostname==='localhost')return original(input,init);
 throw new Error('External access is disabled in route qualification');
};`,
    { mode: 0o600 },
  )
  execFileSync("npm", ["run", "build"], {
    cwd: root,
    env: {
      ...baseEnvironment,
      DEVICE_ACCESS_MODE: "user",
      NEXTAUTH_URL: "http://127.0.0.1:3000",
    },
    timeout: 120000,
    stdio: "pipe",
  })
  let origin = await start("user")
  const personal = await request(origin, "alice", "/api/devices", {
    name: "Camera",
  })
  assert.equal(personal.status, 201)
  assert.equal(
    (
      await request(
        origin,
        "alice",
        `/api/devices/${personal.body.device.id}/recordings`,
      )
    ).status,
    204,
  )
  assert.equal(
    (
      await request(
        origin,
        "alice",
        `/api/devices/${personal.body.device.id}/recordings/playback`,
      )
    ).status,
    404,
  )

  assert.equal(
    (
      await request(
        origin,
        "alice",
        `/api/devices/${personal.body.device.id}/metrics`,
      )
    ).status,
    204,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}/metrics`,
      )
    ).status,
    404,
  )
  assert.equal(
    (await request(origin, "bob", "/api/devices")).body.devices.length,
    0,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}/viewer`,
        {},
      )
    ).status,
    404,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}`,
        null,
        "DELETE",
      )
    ).status,
    404,
  )
  assert.equal(
    (
      await request(
        origin,
        "alice",
        "/api/devices",
        { name: "Cross origin" },
        "POST",
        true,
      )
    ).status,
    403,
  )
  const personalWatch = await request(origin, "alice", "/api/rstream/watch")
  assert.equal(personalWatch.status, 200)
  const decode = (token) =>
    JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString())
  const personalClaims = JSON.stringify(decode(personalWatch.body.auth.token))
  assert.match(personalClaims, /"user":"alice"/)
  assert.doesNotMatch(personalClaims, /"organization"/)
  origin = await start("organization")
  const lock = new Client({
    connectionString: baseEnvironment.POSTGRES_PRISMA_DIRECT_URL,
  })
  await lock.connect()
  try {
    await lock.query("SELECT pg_advisory_lock(hashtextextended($1, 0))", [
      "organization:42",
    ])
    const startedAt = Date.now()
    const blocked = await request(origin, "alice", "/api/devices", {
      name: "Blocked creation",
    })
    assert.equal(blocked.status, 503)
    assert.ok(
      Date.now() - startedAt < 5000,
      "owner lock must have a finite wait",
    )
  } finally {
    await lock.end()
  }
  assert.equal(
    (await request(origin, "alice", "/api/devices")).body.devices.length,
    0,
  )
  const shared = await request(origin, "alice", "/api/devices", {
    name: "Camera",
  })
  assert.equal(shared.status, 201)
  const bobList = await request(origin, "bob", "/api/devices")
  assert.equal(bobList.status, 200)
  assert.equal(bobList.body.devices[0].id, shared.body.device.id)
  const provisioning = await fetch(`${origin}/api/devices/tunnel`, {
    method: "POST",
    headers: { Authorization: `Bearer ${shared.body.secret}` },
    signal: AbortSignal.timeout(10000),
  })
  assert.equal(provisioning.status, 200)
  const producerPayload = await provisioning.json()
  assert.equal(producerPayload.labels.organization, "42")
  assert.equal(producerPayload.labels.user, undefined)
  const producerClaims = JSON.stringify(decode(producerPayload.token))
  assert.match(producerClaims, /"organization":"42"/)
  assert.doesNotMatch(producerClaims, /"user"/)
  assert.equal(
    (await request(origin, "bob", "/api/devices", { name: "Camera" })).status,
    409,
  )
  assert.equal((await request(origin, "outsider", "/api/devices")).status, 403)
  assert.equal(
    (
      await request(
        origin,
        "outsider",
        `/api/devices/${shared.body.device.id}/viewer`,
        {},
      )
    ).status,
    403,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}/quality`,
      )
    ).status,
    404,
  )
  const watch = await request(origin, "bob", "/api/rstream/watch")
  assert.equal(watch.status, 200)
  const claims = JSON.stringify(decode(watch.body.auth.token))
  assert.match(claims, /"organization":"42"/)
  assert.doesNotMatch(claims, /"user"/)
  // A database DDL lock must not leave an application query waiting forever.
  const databaseLock = new Client({
    connectionString: baseEnvironment.POSTGRES_PRISMA_DIRECT_URL,
  })
  await databaseLock.connect()
  try {
    await databaseLock.query(
      "BEGIN; LOCK TABLE devices IN ACCESS EXCLUSIVE MODE",
    )
    const startedAt = Date.now()
    const response = await fetch(`${origin}/api/devices`, {
      headers: { Cookie: `next-auth.session-token=${sessions.bob}` },
      signal: AbortSignal.timeout(8000),
    })
    await response.arrayBuffer()
    assert.equal(response.status, 500)
    assert.ok(
      Date.now() - startedAt < 7500,
      "SQL statements must have a finite wait",
    )
  } finally {
    await databaseLock.query("ROLLBACK")
    await databaseLock.end()
  }
  assert.equal((await request(origin, "bob", "/api/devices")).status, 200)
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${shared.body.device.id}`,
        null,
        "DELETE",
      )
    ).status,
    200,
  )
  baseEnvironment = {
    ...baseEnvironment,
    ...generateMediaMTXKeys("qualification"),
    VIDEO_DISTRIBUTOR: "mediamtx",
    MEDIAMTX_EXPOSURE: "rstream",
    MEDIAMTX_PUBLIC_URL: "",
    MEDIAMTX_TUNNEL_NAME: "qualification-media",
    MEDIAMTX_ALLOW_DIRECT_FALLBACK: "false",
    MEDIAMTX_METRICS_URL: "http://metrics.qualification.invalid/metrics",
    MEDIAMTX_PLAYBACK_URL: "http://playback.qualification.invalid",
  }
  origin = await start("user")
  assert.equal(
    (
      await request(
        origin,
        "alice",
        `/api/devices/${personal.body.device.id}/metrics`,
      )
    ).status,
    200,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}/metrics`,
      )
    ).status,
    404,
    "A warm metrics cache never bypasses ownership",
  )
  const recordingPath = `/api/devices/${personal.body.device.id}/recordings`
  const index = await request(origin, "alice", recordingPath)
  assert.equal(index.status, 200)
  assert.equal(index.body.spans.length, 1)
  assert.equal(JSON.stringify(index.body).includes("private.invalid"), false)
  assert.equal(
    (await request(origin, "bob", recordingPath)).status,
    404,
    "Index cache does not bypass personal ownership",
  )
  assert.equal(
    (await request(origin, "alice", `${recordingPath}?path=other`)).status,
    400,
  )
  const clipQuery = new URLSearchParams({
    start: index.body.spans[0].start,
    duration: "5",
  })
  const clipURL = `${origin}${recordingPath}/playback?${clipQuery}`
  const clip = await fetch(clipURL, {
    headers: {
      Cookie: `next-auth.session-token=${sessions.alice}`,
      Range: "bytes=0-1",
    },
  })
  assert.equal(clip.status, 200)
  assert.equal(clip.headers.get("content-type"), "video/mp4")
  assert.equal(clip.headers.get("accept-ranges"), "none")
  assert.equal(await clip.text(), "test-media")
  assert.equal(
    (await request(origin, "bob", `${recordingPath}/playback?${clipQuery}`))
      .status,
    404,
  )
  const unauthenticated = await fetch(clipURL)
  assert.equal(unauthenticated.status, 401)
  await unauthenticated.body?.cancel()
  assert.equal(
    (
      await request(
        origin,
        "alice",
        `${recordingPath}/playback?start=invalid&duration=31`,
      )
    ).status,
    400,
  )
  origin = await start("organization")
  const requiredMedia = await request(origin, "alice", "/api/devices", {
    name: "Required media",
  })
  assert.equal(requiredMedia.status, 201)
  const mediaMetrics = await request(
    origin,
    "alice",
    `/api/devices/${requiredMedia.body.device.id}/metrics`,
  )
  assert.equal(mediaMetrics.status, 200)
  const orgRecordingPath = `/api/devices/${requiredMedia.body.device.id}/recordings`
  assert.equal((await request(origin, "alice", orgRecordingPath)).status, 200)
  assert.equal((await request(origin, "bob", orgRecordingPath)).status, 200)
  assert.equal(
    (await request(origin, "outsider", orgRecordingPath)).status,
    403,
  )

  assert.equal(mediaMetrics.body.readers, 2)
  assert.equal(mediaMetrics.body.inboundBitsPerSecond, null)
  assert.deepEqual(
    Object.keys(mediaMetrics.body).sort(),
    [
      "state",
      "readers",
      "inboundBitsPerSecond",
      "outboundBitsPerSecond",
      "sampledAt",
      "intervalMs",
    ].sort(),
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${requiredMedia.body.device.id}/metrics`,
      )
    ).status,
    200,
  )
  assert.equal(
    (
      await request(
        origin,
        "outsider",
        `/api/devices/${requiredMedia.body.device.id}/metrics`,
      )
    ).status,
    403,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${requiredMedia.body.device.id}/viewer?distribution=direct`,
        {},
      )
    ).status,
    403,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${requiredMedia.body.device.id}/viewer`,
        {},
      )
    ).status,
    503,
  )
  // Project-native discovery never needs a provisioned Device or secret.
  const discoveryID = randomUUID()
  const discoveryTunnel = {
    id: "discovery-tunnel-1",
    client_id: "discovery-producer",
    project_id: "qualification-project",
    status: "online",
    protocol: "http",
    publish: true,
    token_auth: true,
    host: "video.qualification.invalid",
    name: "video-session-1",
    labels: {
      app: "webrtc-video-platform",
      inventory: "discovered",
      device: discoveryID,
      "device-name": "Front camera",
    },
  }
  baseEnvironment.DEVICE_INVENTORY_MODE = "discovered"
  baseEnvironment.RSTREAM_PROJECT_ENDPOINT = "qualification-endpoint"
  setInventory([discoveryTunnel])
  origin = await start("organization")
  const discovered = await request(origin, "alice", "/api/devices")
  assert.equal(discovered.status, 200)
  assert.equal(
    (await request(origin, "alice", `/api/devices/${discoveryID}/metrics`))
      .status,
    200,
  )
  assert.equal(
    (await request(origin, "outsider", `/api/devices/${discoveryID}/metrics`))
      .status,
    403,
  )
  assert.equal(discovered.body.devices.length, 1)
  assert.equal(discovered.body.devices[0].name, "Front camera")
  assert.equal(discovered.body.devices[0].secretPrefix, null)
  assert.equal(discovered.body.devices[0].inventory, "discovered")
  assert.equal(discovered.body.devices[0].online, true)
  assert.deepEqual(
    (await request(origin, "bob", "/api/devices")).body.devices.map(
      (d) => d.id,
    ),
    [discoveryID],
  )
  assert.equal((await request(origin, "outsider", "/api/devices")).status, 403)
  assert.equal(
    (await request(origin, "bob", "/api/devices", { name: "Cannot enroll" }))
      .status,
    409,
  )
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${discoveryID}`,
        null,
        "DELETE",
      )
    ).status,
    409,
  )
  const deniedProvisioning = await fetch(`${origin}/api/devices/tunnel`, {
    method: "POST",
    headers: { Authorization: `Bearer ${shared.body.secret}` },
    signal: AbortSignal.timeout(10000),
  })
  assert.equal(deniedProvisioning.status, 409)
  await deniedProvisioning.arrayBuffer()
  assert.equal(
    (await request(origin, "alice", `/api/devices/${discoveryID}/quality`))
      .status,
    200,
  )
  async function resolveDiscoveredSource(
    path = `devices/${discoveryID}`,
    signedPath = path,
    purpose = "signaling",
  ) {
    const header = Buffer.from(
      JSON.stringify({
        alg: "EdDSA",
        typ: "JWT",
        kid: JSON.parse(baseEnvironment.MEDIAMTX_SOURCE_RESOLVER_JWKS).keys[0]
          .kid,
      }),
    ).toString("base64url")
    const now = Math.floor(Date.now() / 1000)
    const payload = Buffer.from(
      JSON.stringify({
        aud: "rstream-video-source-resolver",
        iss: "rstream-video-distributor",
        sub: "qualification",
        path: signedPath,
        purpose,
        iat: now,
        nbf: now - 5,
        exp: now + 20,
        jti: randomBytes(16).toString("base64url"),
      }),
    ).toString("base64url")
    const input = `${header}.${payload}`
    const key = createPrivateKey({
      key: Buffer.from(
        baseEnvironment.RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64,
        "base64",
      ),
      type: "pkcs8",
      format: "der",
    })
    const authorization = `Bearer ${input}.${sign(null, Buffer.from(input), key).toString("base64url")}`
    const response = await fetch(`${origin}/api/video/distributor/source`, {
      method: "POST",
      headers: {
        Authorization: authorization,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ path, purpose }),
      signal: AbortSignal.timeout(10000),
    })
    return {
      status: response.status,
      body: response.status === 204 ? null : await response.json(),
    }
  }
  resetCalls()
  const resolvedSource = await resolveDiscoveredSource()
  assert.equal(resolvedSource.status, 200)
  assert.deepEqual(
    upstreamCalls(),
    ["inventory", "project"],
    "Signaling resolves the project and live source once, without TURN",
  )
  for (let attempt = 0; attempt < 2; attempt++) {
    resetCalls()
    const session = await resolveDiscoveredSource(
      undefined,
      undefined,
      "session",
    )
    assert.equal(session.status, 200)
    assert.equal(session.body.iceServers.length, 1)
    assert.deepEqual(
      upstreamCalls(),
      ["inventory", "keyring", "project"],
      "Each source session rechecks live inventory and resolves its project exactly once",
    )
    resetCalls()
    const viewer = await request(
      origin,
      "alice",
      `/api/devices/${discoveryID}/viewer`,
      {},
    )
    assert.equal(viewer.status, 200)
    assert.deepEqual(
      upstreamCalls(),
      ["distribution", "inventory", "keyring", "project"],
      "Each viewer rechecks source and distributor inventory while resolving its project exactly once",
    )
  }
  const sourceURL = new URL(resolvedSource.body.url)
  assert.equal(sourceURL.pathname, "/whep")
  const sourceClaims = JSON.stringify(
    decode(sourceURL.searchParams.get("rstream.token")),
  )
  assert.match(sourceClaims, /"inventory":"discovered"/)
  assert.match(sourceClaims, /discovery-tunnel-1/)
  assert.doesNotMatch(sourceClaims, /api\/quality/)
  assert.equal(
    (
      await resolveDiscoveredSource(
        `devices/${discoveryID}`,
        `devices/${randomUUID()}`,
      )
    ).status,
    401,
  )
  const discoveredWatch = await request(origin, "alice", "/api/rstream/watch")
  const discoveryClaims = JSON.stringify(
    decode(discoveredWatch.body.auth.token),
  )
  assert.match(discoveryClaims, /"inventory":"discovered"/)
  assert.doesNotMatch(discoveryClaims, /"organization"|"user"/)
  assert.equal(
    (
      await request(
        origin,
        "bob",
        `/api/devices/${personal.body.device.id}/quality`,
      )
    ).status,
    404,
  )
  const renamed = {
    ...discoveryTunnel,
    id: "discovery-tunnel-2",
    labels: { ...discoveryTunnel.labels, "device-name": "Rear camera" },
  }
  setInventory([renamed])
  await Promise.all(
    Array.from({ length: 8 }, async () => {
      const listed = await request(origin, "bob", "/api/devices")
      assert.equal(listed.status, 200)
      assert.equal(listed.body.devices.length, 1)
      assert.equal(listed.body.devices[0].id, discoveryID)
      assert.equal(listed.body.devices[0].name, "Rear camera")
    }),
  )
  if (process.env.RSTREAM_DISCOVERY_BROWSER) {
    const { chromium, firefox, webkit } = await import("playwright-core")
    if (process.env.RSTREAM_RECORDING_BROWSER === "1") {
      for (const [name, type, options] of [
        [
          "recording-chromium",
          chromium,
          { executablePath: process.env.RSTREAM_DISCOVERY_BROWSER },
        ],
        ["recording-firefox", firefox, {}],
        ["recording-webkit", webkit, {}],
      ]) {
        const browser = await launchBrowserContext(type, options)
        try {
          const { context } = browser
          await context.addCookies([
            {
              name: "next-auth.session-token",
              value: sessions.bob,
              url: origin,
            },
          ])
          await qualifyFullPage({
            context,
            origin,
            name,
            directory: process.env.RSTREAM_UI_CAPTURE_DIRECTORY,
            observeRecordings: true,
          })
        } finally {
          await browser.close()
        }
      }
    }
    if (process.env.RSTREAM_METRICS_BROWSER === "1") {
      const browser = await chromium.launch({
        executablePath: process.env.RSTREAM_DISCOVERY_BROWSER,
        headless: true,
      })
      try {
        const context = await browser.newContext()
        await context.addCookies([
          { name: "next-auth.session-token", value: sessions.bob, url: origin },
        ])
        await qualifyFullPage({
          context,
          origin,
          name: "metrics-chromium",
          directory: process.env.RSTREAM_UI_CAPTURE_DIRECTORY,
          observeDistribution: true,
        })
      } finally {
        await browser.close()
      }
    }
    if (process.env.RSTREAM_FULL_PAGE_BROWSERS === "1") {
      const failures = []
      for (const [name, type, options] of [
        [
          "chromium",
          chromium,
          { executablePath: process.env.RSTREAM_DISCOVERY_BROWSER },
        ],
        ["firefox", firefox, {}],
        ["webkit", webkit, {}],
      ]) {
        const browser = await launchBrowserContext(type, options)
        try {
          const { context } = browser
          await context.addCookies([
            {
              name: "next-auth.session-token",
              value: sessions.bob,
              url: origin,
            },
          ])
          await qualifyFullPage({
            context,
            origin,
            name,
            directory: process.env.RSTREAM_UI_CAPTURE_DIRECTORY,
          })
        } catch (error) {
          failures.push(error)
        } finally {
          await browser.close()
        }
      }
      if (failures.length)
        throw new AggregateError(
          failures,
          "Full-page browser qualification failed",
        )
    }
    const browser = await chromium.launch({
      executablePath: process.env.RSTREAM_DISCOVERY_BROWSER,
      headless: true,
    })
    try {
      const context = await browser.newContext()
      await context.addCookies([
        { name: "next-auth.session-token", value: sessions.bob, url: origin },
      ])
      const page = await context.newPage()
      await page.goto(origin)
      await page
        .getByRole("heading", { name: "Rear camera", exact: true })
        .waitFor()
      assert.equal(
        await page
          .getByRole("button", {
            name: /Add device|Create device/,
            exact: false,
          })
          .count(),
        0,
      )
      assert.equal(
        await page
          .getByRole("button", { name: "Delete Rear camera", exact: true })
          .count(),
        0,
      )
      setInventory([
        {
          ...renamed,
          labels: { ...renamed.labels, "device-name": "Inspection camera" },
        },
      ])
      await page
        .getByRole("heading", { name: "Inspection camera", exact: true })
        .waitFor({ timeout: 15000 })
      setInventory({ error: true })
      assert.equal(
        (await request(origin, "bob", `/api/devices/${discoveryID}/recordings`))
          .status,
        503,
      )

      await page
        .getByText("Live inventory cannot be confirmed.", { exact: false })
        .waitFor({ timeout: 15000 })
      assert.ok((await page.getByText("Unknown", { exact: true }).count()) > 0)
      setInventory([])
      await page
        .getByText("Live inventory cannot be confirmed.", { exact: false })
        .waitFor({ state: "hidden", timeout: 15000 })
      assert.ok((await page.getByText("Offline", { exact: true }).count()) > 0)
      if (process.env.RSTREAM_UI_CAPTURE_DIRECTORY) {
        const directory = resolve(process.env.RSTREAM_UI_CAPTURE_DIRECTORY)
        mkdirSync(directory, { recursive: true })
        await page.setViewportSize({ width: 1440, height: 1100 })
        await page.screenshot({
          path: join(directory, "discovery-desktop.png"),
          fullPage: true,
        })
        await page.setViewportSize({ width: 390, height: 844 })
        await page.screenshot({
          path: join(directory, "discovery-mobile.png"),
          fullPage: true,
        })
      }
      await context.close()
      console.log(
        "PASS: discovery browser hydration, live rename, unavailable/recovery state and hidden provisioning controls",
      )
    } finally {
      await browser.close()
    }
    setInventory([renamed])
  }
  // A late poll must not replace newer metadata or observation timestamps.
  const historyDB = new Client({ connectionString: db })
  await historyDB.connect()
  try {
    await historyDB.query(
      `UPDATE discovered_devices SET name='Newer observation', "lastSeenAt"=now()+interval '1 minute' WHERE "deviceId"=$1`,
      [discoveryID],
    )
    assert.equal(
      (await request(origin, "bob", "/api/devices")).body.devices[0].name,
      "Newer observation",
    )
    const count = await historyDB.query(
      "SELECT count(*)::int AS count FROM discovered_devices",
    )
    assert.equal(count.rows[0].count, 1)
  } finally {
    await historyDB.end()
  }
  setInventory([discoveryTunnel, renamed])
  assert.equal((await request(origin, "bob", "/api/devices")).status, 409)
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/quality`))
      .status,
    409,
  )
  setInventory({ error: true })
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/metrics`))
      .status,
    503,
    "A warm cache never bypasses live discovery",
  )
  assert.equal((await request(origin, "bob", "/api/devices")).status, 503)
  setInventory([])
  const offline = await request(origin, "bob", "/api/devices")
  assert.equal(offline.body.devices[0].online, false)
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/recordings`))
      .status,
    404,
    "History alone does not authorize recorded media",
  )

  assert.ok(offline.body.devices[0].lastSeenAt)
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/metrics`))
      .status,
    404,
  )
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/quality`))
      .status,
    404,
  )
  assert.equal(
    (await request(origin, "bob", `/api/devices/${discoveryID}/viewer`, {}))
      .status,
    404,
  )
  assert.equal(
    (await resolveDiscoveredSource()).status,
    404,
    "history alone cannot authorize a MediaMTX source",
  )
  baseEnvironment.DEVICE_DISCOVERY_HISTORY_ENABLED = "false"
  origin = await start("organization")
  assert.equal(
    (await request(origin, "bob", "/api/devices")).body.devices.length,
    0,
  )
  if (
    process.env.RSTREAM_DISCOVERY_BROWSER &&
    process.env.RSTREAM_UI_CAPTURE_DIRECTORY
  ) {
    const { chromium } = await import("playwright-core")
    const browser = await chromium.launch({
      executablePath: process.env.RSTREAM_DISCOVERY_BROWSER,
      headless: true,
    })
    try {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 1100 },
      })
      await context.addCookies([
        { name: "next-auth.session-token", value: sessions.bob, url: origin },
      ])
      const page = await context.newPage()
      await page.goto(origin)
      await page
        .getByRole("heading", { name: "No device selected", exact: true })
        .waitFor()
      await page.screenshot({
        path: join(
          resolve(process.env.RSTREAM_UI_CAPTURE_DIRECTORY),
          "discovery-empty.png",
        ),
        fullPage: true,
      })
    } finally {
      await browser.close()
    }
  }
  const ephemeral = {
    ...renamed,
    labels: { ...renamed.labels, device: randomUUID() },
  }
  setInventory([ephemeral])
  assert.equal(
    (await request(origin, "bob", "/api/devices")).body.devices[0].id,
    ephemeral.labels.device,
  )
  const readOnlyHistory = new Client({ connectionString: db })
  await readOnlyHistory.connect()
  try {
    const count = await readOnlyHistory.query(
      "SELECT count(*)::int AS count FROM discovered_devices",
    )
    assert.equal(
      count.rows[0].count,
      1,
      "live-only discovery must not write device history",
    )
  } finally {
    await readOnlyHistory.end()
  }
  setInventory([{ ...ephemeral, project_id: "another-project" }])
  assert.equal(
    (await request(origin, "bob", "/api/devices")).body.devices.length,
    0,
  )
  console.log(
    "PASS: project discovery, display names, concurrent history, stale observations, live-only inventory, outages and access scoping",
  )
  console.log(
    "PASS: real Next.js user isolation, shared organization inventory, nonmember denial, mutation origin and signed watch-token scope",
  )
} catch (error) {
  if (output) console.error(output)
  throw error
} finally {
  await stop()
  if (started) docker("rm", "-f", name)
  rmSync(runtime, { recursive: true, force: true })
}
