// Real Next.js routes and PostgreSQL; only upstream GitHub/engine HTTP is mocked.
import assert from "node:assert/strict"
import { execFileSync, spawn } from "node:child_process"
import { generateKeyPairSync, randomUUID } from "node:crypto"
import { mkdtempSync, writeFileSync, rmSync } from "node:fs"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { setTimeout as delay } from "node:timers/promises"
import { generateMediaMTXKeys } from "./generate-mediamtx-key.mjs"
import { Client } from "pg"

const root = resolve(import.meta.dirname, "..")
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
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
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
  return { status: response.status, body: await response.json() }
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
      docker("exec", name, "pg_isready", "-U", "postgres")
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
    RSTREAM_CLIENT_ID: "qualification",
    RSTREAM_CLIENT_SECRET: privateKey,
    RSTREAM_PROJECT_ENDPOINT: "",
    RSTREAM_PROJECT_ID: "qualification-project",
    RSTREAM_ENGINE: "engine.qualification.invalid:443",
    VIDEO_DISTRIBUTOR: "direct",
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
  writeFileSync(
    join(runtime, "upstreams.mjs"),
    `const original=globalThis.fetch.bind(globalThis);globalThis.fetch=async(input,init)=>{
 const request=new Request(input,init),url=new URL(request.url);
 if(url.hostname==='api.github.com'){
  const actor=request.headers.get('authorization')?.split('member-')[1];
  return Response.json({state:actor==='outsider'?'pending':'active',organization:{id:42,login:'acme'},user:{id:actor==='alice'?7:actor==='bob'?8:9}});
 }
 if(url.hostname==='engine.qualification.invalid')return Response.json([]);
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
  }
  origin = await start("organization")
  const requiredMedia = await request(origin, "alice", "/api/devices", {
    name: "Required media",
  })
  assert.equal(requiredMedia.status, 201)
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
