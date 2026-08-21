#!/usr/bin/env -S node --env-file-if-exists=.env.local

import { spawn } from "node:child_process"
import { createSocket } from "node:dgram"
import { mkdtemp, rm, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import process from "node:process"
import { fileURLToPath, pathToFileURL } from "node:url"
import { setTimeout as delay } from "node:timers/promises"

import { RstreamTunnelsClient } from "@rstreamlabs/tunnels"

import { generateMediaMTXKeys } from "./generate-mediamtx-key.mjs"

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const platformDirectory = dirname(scriptDirectory)
const distributorDirectory = join(platformDirectory, "..", "distributor")
const image = "rstream-video-distributor:local"
const issuer = "rstream-webrtc-video-platform"
const audience = "rstream-mediamtx"
const resolverIssuer = "rstream-video-distributor"
const resolverAudience = "rstream-video-source-resolver"

export function localTunnelNames(suffix) {
  return {
    distributor: `webrtc-video-mediamtx-${suffix}`,
    platform: `webrtc-video-platform-${suffix}`,
  }
}

export function localStackOptions(args) {
  if (args.length === 0) {
    return { exposure: "public" }
  }
  if (
    args.length === 2 &&
    args[0] === "--exposure" &&
    new Set(["public", "rstream"]).has(args[1])
  ) {
    return { exposure: args[1] }
  }
  throw new Error("usage: run-local-mediamtx.mjs [--exposure public|rstream]")
}

export function tunnelResources(names, exposure) {
  const create = (name, tokenAuth) => ({
    scopes: {
      tunnels: {
        create: {
          filters: {
            name: { exact: name },
            protocol: "http",
            publish: true,
            token_auth: tokenAuth,
          },
        },
      },
    },
  })
  const resources = [create(names.platform, false)]
  if (exposure === "rstream") {
    resources.push(create(names.distributor, true))
  }
  return {
    tunnels: {
      OR: resources,
    },
  }
}

export function tunnelConfiguration(names, exposure) {
  const lines = [
    "version: 1",
    "tunnels:",
    `  - name: ${names.platform}`,
    "    forward: 127.0.0.1:3000",
    "    tunnel:",
    "      publish: true",
    "      protocol: http",
    "      http:",
    "        version: http/1.1",
    "        auth:",
    "          token: false",
    "          rstream: false",
  ]
  if (exposure === "rstream") {
    lines.push(
      `  - name: ${names.distributor}`,
      "    forward: 127.0.0.1:8889",
      "    tunnel:",
      "      publish: true",
      "      protocol: http",
      "      http:",
      "        version: http/1.1",
      "        auth:",
      "          token: true",
      "          rstream: false",
    )
  }
  lines.push("")
  return lines.join("\n")
}

export function mediaMTXEnvironment(platformHost, keys) {
  const platformOrigin = `https://${platformHost}`
  return {
    MTX_AUTHJWTAUDIENCE: audience,
    MTX_AUTHJWTISSUER: issuer,
    MTX_AUTHJWTJWKS: `${platformOrigin}/api/video/distributor/jwks`,
    MTX_PATHDEFAULTS_MAXREADERS: "8",
    MTX_WEBRTCADDITIONALHOSTS: "127.0.0.1",
    MTX_WEBRTCALLOWORIGINS: "http://localhost:3000",
    RSTREAM_MEDIAMTX_URL: "http://127.0.0.1:8889",
    RSTREAM_SOURCE_RESOLVER_AUDIENCE: resolverAudience,
    RSTREAM_SOURCE_RESOLVER_INSTANCE_ID:
      keys.RSTREAM_SOURCE_RESOLVER_INSTANCE_ID,
    RSTREAM_SOURCE_RESOLVER_ISSUER: resolverIssuer,
    RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64:
      keys.RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64,
    RSTREAM_SOURCE_RESOLVER_URL: `${platformOrigin}/api/video/distributor/source`,
  }
}

export function installStopSignalHandlers(target = process) {
  const handlers = new Map()
  let resolveStop
  let stopping = false
  const promise = new Promise((resolve) => {
    resolveStop = resolve
  })
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    const handler = () => {
      if (!stopping) {
        stopping = true
        resolveStop(signal)
      }
    }
    handlers.set(signal, handler)
    target.on(signal, handler)
  }
  return {
    promise,
    remove() {
      for (const [signal, handler] of handlers) {
        target.removeListener(signal, handler)
      }
    },
  }
}

function requiredEnvironment() {
  for (const name of [
    "GITHUB_CLIENT_ID",
    "GITHUB_CLIENT_SECRET",
    "NEXTAUTH_SECRET",
    "NEXTAUTH_URL",
    "POSTGRES_PRISMA_DIRECT_URL",
    "POSTGRES_PRISMA_POOL_URL",
    "RSTREAM_CLIENT_ID",
    "RSTREAM_CLIENT_SECRET",
    "RSTREAM_PROJECT_ENDPOINT",
  ]) {
    if (!process.env[name]?.trim()) {
      throw new Error(`${name} is required in platform/.env.local`)
    }
  }
  const nextAuthURL = new URL(process.env.NEXTAUTH_URL)
  if (
    nextAuthURL.protocol !== "http:" ||
    !new Set(["127.0.0.1", "localhost"]).has(nextAuthURL.hostname) ||
    nextAuthURL.port !== "3000"
  ) {
    throw new Error(
      "NEXTAUTH_URL must be http://localhost:3000 for the local MediaMTX stack",
    )
  }
}

function rstreamClient() {
  return new RstreamTunnelsClient({
    apiUrl: process.env.RSTREAM_API_URL ?? "https://rstream.io",
    credentials: {
      clientId: process.env.RSTREAM_CLIENT_ID,
      clientSecret: process.env.RSTREAM_CLIENT_SECRET,
    },
    projectId: process.env.RSTREAM_PROJECT_ID,
    projectEndpoint: process.env.RSTREAM_PROJECT_ENDPOINT,
  })
}

async function issueAgentToken(client, names, exposure) {
  const engine = await client.getEngine()
  const issued = await client.auth.createAuthToken({
    expires_in: 3600,
    resources: tunnelResources(names, exposure),
  })
  return { engine, token: issued.token }
}

async function createRuntimeContext(runtimeDirectory, credentials) {
  const config = join(runtimeDirectory, "rstream.yaml")
  await runCommand(
    "rstream",
    [
      "--config",
      config,
      "context",
      "create",
      "mediamtx-local",
      "--engine",
      credentials.engine,
      "--token-stdin",
      "--project-endpoint",
      process.env.RSTREAM_PROJECT_ENDPOINT,
    ],
    { input: credentials.token },
  )
  return config
}

async function waitForHTTP(url, timeoutMilliseconds = 60_000) {
  const deadline = Date.now() + timeoutMilliseconds
  let lastError
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { redirect: "error" })
      if (response.ok) {
        return response
      }
      lastError = new Error(`${url} returned ${response.status}`)
    } catch (err) {
      lastError = err
    }
    await delay(500)
  }
  throw new Error(`Timed out waiting for ${url}`, { cause: lastError })
}

export async function requireAvailablePort(port) {
  await new Promise((resolve, reject) => {
    const server = createServer()
    server.unref()
    server.once("error", (err) => {
      reject(new Error(`TCP port ${port} is already in use`, { cause: err }))
    })
    server.listen({ host: "127.0.0.1", port }, () => {
      server.close((err) => {
        if (err) {
          reject(err)
          return
        }
        resolve()
      })
    })
  })
}

export async function requireAvailableUDPPort(port) {
  await new Promise((resolve, reject) => {
    const socket = createSocket("udp4")
    const close = () => socket.close()
    socket.unref()
    socket.once("error", (err) => {
      reject(new Error(`UDP port ${port} is already in use`, { cause: err }))
    })
    socket.once("listening", () => {
      socket.once("close", resolve)
      close()
    })
    socket.bind(port, "127.0.0.1")
  })
}

async function waitForTunnels(client, names, exposure) {
  const deadline = Date.now() + 60_000
  while (Date.now() < deadline) {
    const filters = (name) => ({
      name,
      protocol: "http",
      publish: true,
      status: "online",
    })
    const [platformTunnels, distributorTunnels] = await Promise.all([
      client.tunnels.list({ limit: 2, filters: filters(names.platform) }),
      exposure === "rstream"
        ? client.tunnels.list({ limit: 2, filters: filters(names.distributor) })
        : Promise.resolve([]),
    ])
    const platform = platformTunnels.find(
      (tunnel) => tunnel.name === names.platform,
    )
    const distributor = distributorTunnels.find(
      (tunnel) => tunnel.name === names.distributor,
    )
    const platformHost = platform?.host ?? platform?.hostname
    const distributorHost = distributor?.host ?? distributor?.hostname
    if (platformHost && (exposure === "public" || distributorHost)) {
      return { distributorHost, platformHost }
    }
    await delay(500)
  }
  throw new Error("Required local rstream tunnels did not become online")
}

async function waitForHealthyContainer(name) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    const result = await captureCommand("docker", [
      "inspect",
      "--format",
      "{{.State.Health.Status}}",
      name,
    ])
    if (result.stdout.trim() === "healthy") {
      return
    }
    await delay(500)
  }
  throw new Error("MediaMTX container did not become healthy")
}

function startProcess(command, args, options = {}) {
  const child = spawn(command, args, {
    cwd: options.cwd,
    detached: true,
    env: options.env ?? process.env,
    stdio: "inherit",
  })
  child.on("error", (err) => {
    console.error(`${command} failed to start:`, err)
  })
  return child
}

function childExit(child, name) {
  return new Promise((_, reject) => {
    child.once("error", reject)
    child.once("exit", (code, signal) => {
      reject(
        new Error(
          `${name} stopped unexpectedly (${signal ?? `status ${code ?? "unknown"}`})`,
        ),
      )
    })
  })
}

async function terminateProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) {
    return
  }
  try {
    process.kill(-child.pid, "SIGTERM")
  } catch (err) {
    if (err?.code !== "ESRCH") {
      throw err
    }
    return
  }
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(10_000),
  ])
  if (child.exitCode === null && child.signalCode === null) {
    try {
      process.kill(-child.pid, "SIGKILL")
    } catch (err) {
      if (err?.code !== "ESRCH") {
        throw err
      }
    }
  }
}

function runCommand(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env ?? process.env,
      stdio: [
        options.input === undefined ? "inherit" : "pipe",
        "inherit",
        "inherit",
      ],
    })
    child.once("error", reject)
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolve()
        return
      }
      reject(
        new Error(
          `${command} failed (${signal ?? `status ${code ?? "unknown"}`})`,
        ),
      )
    })
    if (options.input !== undefined) {
      child.stdin.end(options.input)
    }
  })
}

function captureCommand(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env ?? process.env,
      stdio: ["ignore", "pipe", "pipe"],
    })
    let stdout = ""
    let stderr = ""
    child.stdout.setEncoding("utf8")
    child.stderr.setEncoding("utf8")
    child.stdout.on("data", (value) => {
      stdout += value
    })
    child.stderr.on("data", (value) => {
      stderr += value
    })
    child.once("error", reject)
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolve({ stderr, stdout })
        return
      }
      reject(
        new Error(
          `${command} failed (${signal ?? `status ${code ?? "unknown"}`}): ${stderr.trim()}`,
        ),
      )
    })
  })
}

async function runLocalMediaMTX() {
  const options = localStackOptions(process.argv.slice(2))
  requiredEnvironment()
  const suffix = `local-${process.pid}`
  const names = localTunnelNames(suffix)
  const containerName = `rstream-video-mediamtx-${suffix}`
  const runtimeDirectory = await mkdtemp(join(tmpdir(), "rstream-video-local."))
  const tunnelsPath = join(runtimeDirectory, "tunnels.yaml")
  const keys = generateMediaMTXKeys(suffix)
  const client = rstreamClient()
  let distributorStarted = false
  let nextProcess
  let runnerProcess
  const stop = installStopSignalHandlers()
  try {
    const credentials = await issueAgentToken(client, names, options.exposure)
    const configPath = await createRuntimeContext(runtimeDirectory, credentials)
    await writeFile(tunnelsPath, tunnelConfiguration(names, options.exposure), {
      mode: 0o600,
    })
    await runCommand("docker", ["build", "-t", image, "."], {
      cwd: distributorDirectory,
    })
    await Promise.all(
      [3000, 8889, 9998, 9999].map((port) => requireAvailablePort(port)),
    )
    await requireAvailableUDPPort(8189)
    await runCommand("npm", ["run", "clean"], { cwd: platformDirectory })
    await runCommand("npm", ["run", "prisma:generate"], {
      cwd: platformDirectory,
    })
    const platformEnvironment = {
      ...process.env,
      MEDIAMTX_JWT_ADDITIONAL_JWKS: '{"keys":[]}',
      MEDIAMTX_JWT_AUDIENCE: audience,
      MEDIAMTX_JWT_ISSUER: issuer,
      MEDIAMTX_JWT_PRIVATE_KEY_BASE64: keys.MEDIAMTX_JWT_PRIVATE_KEY_BASE64,
      MEDIAMTX_EXPOSURE: options.exposure,
      MEDIAMTX_PUBLIC_URL:
        options.exposure === "public" ? "http://localhost:8889" : "",
      MEDIAMTX_SOURCE_RESOLVER_AUDIENCE: resolverAudience,
      MEDIAMTX_SOURCE_RESOLVER_ISSUER: resolverIssuer,
      MEDIAMTX_SOURCE_RESOLVER_JWKS: keys.MEDIAMTX_SOURCE_RESOLVER_JWKS,
      MEDIAMTX_TUNNEL_NAME:
        options.exposure === "rstream" ? names.distributor : "",
      VIDEO_DISTRIBUTOR: "mediamtx",
    }
    nextProcess = startProcess(
      join(platformDirectory, "node_modules", ".bin", "next"),
      ["dev", "--port", "3000"],
      {
        cwd: platformDirectory,
        env: platformEnvironment,
      },
    )
    runnerProcess = startProcess(
      "rstream",
      [
        "--config",
        configPath,
        "--context",
        "mediamtx-local",
        "run",
        "--apply",
        tunnelsPath,
        "--watch",
      ],
      { cwd: platformDirectory },
    )
    const nextFailure = childExit(nextProcess, "Next.js")
    const runnerFailure = childExit(runnerProcess, "rstream tunnel agent")
    await Promise.race([
      waitForHTTP("http://127.0.0.1:3000/api/auth/providers"),
      nextFailure,
    ])
    const { distributorHost, platformHost } = await Promise.race([
      waitForTunnels(client, names, options.exposure),
      nextFailure,
      runnerFailure,
    ])
    await Promise.race([
      waitForHTTP(`https://${platformHost}/api/video/distributor/jwks`),
      nextFailure,
      runnerFailure,
    ])
    const distributorEnvironment = mediaMTXEnvironment(platformHost, keys)
    const dockerArguments = [
      "run",
      "--detach",
      "--name",
      containerName,
      "--init",
      "--read-only",
      "--security-opt",
      "no-new-privileges",
      "--cap-drop",
      "ALL",
      "--tmpfs",
      "/tmp:size=16m,mode=1777",
      "--publish",
      "127.0.0.1:8889:8889/tcp",
      "--publish",
      "127.0.0.1:8189:8189/udp",
      "--publish",
      "127.0.0.1:9998:9998/tcp",
      "--publish",
      "127.0.0.1:9999:9999/tcp",
    ]
    for (const [name, value] of Object.entries(distributorEnvironment)) {
      dockerArguments.push("--env", `${name}=${value}`)
    }
    dockerArguments.push(image)
    await runCommand("docker", dockerArguments)
    distributorStarted = true
    await waitForHealthyContainer(containerName)
    console.log("\nLocal MediaMTX stack is ready.")
    console.log("Platform: http://localhost:3000")
    console.log(
      `MediaMTX: ${
        distributorHost
          ? `https://${distributorHost} through rstream`
          : "http://localhost:8889 directly"
      }`,
    )
    console.log(
      "Create a device, then run the producer command shown by the UI.",
    )
    console.log(
      `Press Ctrl-C here to stop Next.js, MediaMTX, and ${
        options.exposure === "rstream"
          ? "the two temporary tunnels"
          : "the temporary callback tunnel"
      }.\n`,
    )
    await Promise.race([stop.promise, nextFailure, runnerFailure])
  } finally {
    if (distributorStarted) {
      await runCommand("docker", [
        "stop",
        "--timeout",
        "10",
        containerName,
      ]).catch(() => {})
      await runCommand("docker", ["rm", containerName]).catch(() => {})
    }
    await terminateProcess(runnerProcess)
    await terminateProcess(nextProcess)
    await rm(runtimeDirectory, { force: true, recursive: true })
    stop.remove()
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  runLocalMediaMTX().catch((err) => {
    console.error(err)
    process.exitCode = 1
  })
}
