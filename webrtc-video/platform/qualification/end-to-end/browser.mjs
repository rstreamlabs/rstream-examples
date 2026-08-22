import { execFile } from "node:child_process"
import { mkdir, writeFile } from "node:fs/promises"
import { dirname } from "node:path"
import process from "node:process"
import { promisify } from "node:util"

import { chromium } from "playwright-core"

import {
  drainBrowserEvents,
  unexpectedBrowserDiagnostics,
} from "./evidence.mjs"

const exec = promisify(execFile)
const options = parseArguments(process.argv.slice(2))
const startedAt = Date.now()
const events = []
const diagnostics = []
const browserEvents = []
let unexpectedDiagnostics = []
let browser
let page
let distributorStopped = false
try {
  browser = await chromium.launch({
    executablePath: options.browserExecutable,
    headless: true,
  })
  const context = await browser.newContext({
    ignoreHTTPSErrors: false,
    viewport: { height: 900, width: 1440 },
  })
  await context.addCookies([
    {
      httpOnly: true,
      name: "next-auth.session-token",
      sameSite: "Lax",
      secure: new URL(options.platform).protocol === "https:",
      url: options.platform,
      value: options.sessionToken,
    },
  ])
  await context.addInitScript(() => {
    window.__rstreamQualificationEvents = []
    window.addEventListener("rstream:video-distributor-fallback", (event) => {
      window.__rstreamQualificationEvents.push({
        detail: event.detail,
        name: event.type,
        observedAt: performance.now(),
      })
    })
    window.addEventListener("rstream:whep-close", (event) => {
      window.__rstreamQualificationEvents.push({
        detail: event.detail,
        name: event.type,
        observedAt: performance.now(),
      })
    })
  })
  page = await context.newPage()
  page.on("console", (message) => {
    if (new Set(["error", "warning"]).has(message.type())) {
      const location = message.location()
      const source = location.url
        ? `${sanitize(location.url)}:${location.lineNumber}:${location.columnNumber} `
        : ""
      diagnostics.push({
        message: `${source}${sanitize(message.text())}`,
        observedAt: elapsed(startedAt),
        phase: currentPhase(events),
        type: `console-${message.type()}`,
      })
    }
  })
  page.on("pageerror", (error) => {
    diagnostics.push({
      message: sanitize(error.message),
      observedAt: elapsed(startedAt),
      phase: currentPhase(events),
      type: "page-error",
    })
  })
  page.on("response", (response) => {
    if (response.status() >= 400) {
      diagnostics.push({
        message: sanitize(
          `${response.request().method()} ${response.url()} ${response.status()}`,
        ),
        observedAt: elapsed(startedAt),
        phase: currentPhase(events),
        type: "http-error",
      })
    }
  })
  page.on("requestfailed", (request) => {
    diagnostics.push({
      message: sanitize(
        `${request.method()} ${request.url()} ${request.failure()?.errorText ?? "failed"}`,
      ),
      observedAt: elapsed(startedAt),
      phase: currentPhase(events),
      type: "request-failed",
    })
  })
  events.push({ name: "navigation-started", observedAt: elapsed(startedAt) })
  await page.goto(options.platform, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  })
  await waitForText(page, "Distribution path: MediaMTX", 120_000)
  await waitForVideo(page, 30_000)
  const distributed = await observeSustainedPlayback(page, {
    durationMilliseconds: 20_000,
    label: "Distribution path: MediaMTX",
  })
  events.push({
    decodedFrames: distributed.observedFrames,
    framesPerSecond: distributed.framesPerSecond,
    height: distributed.height,
    longestStallMilliseconds: distributed.longestStallMilliseconds,
    name: "mediamtx-playing",
    observedAt: elapsed(startedAt),
    width: distributed.width,
  })
  events.push({
    name: "mediamtx-stop-requested",
    observedAt: elapsed(startedAt),
  })
  await exec("docker", ["stop", "--timeout", "10", options.container])
  distributorStopped = true
  events.push({ name: "mediamtx-stopped", observedAt: elapsed(startedAt) })
  await waitForText(
    page,
    "Distribution path: Direct (MediaMTX fallback)",
    120_000,
  )
  await waitForVideo(page, 30_000)
  const fallback = await observeSustainedPlayback(page, {
    durationMilliseconds: 10_000,
    label: "Distribution path: Direct (MediaMTX fallback)",
  })
  events.push({
    decodedFrames: fallback.observedFrames,
    framesPerSecond: fallback.framesPerSecond,
    height: fallback.height,
    longestStallMilliseconds: fallback.longestStallMilliseconds,
    name: "direct-fallback-playing",
    observedAt: elapsed(startedAt),
    width: fallback.width,
  })
  await drainBrowserEvents(page, browserEvents)
  await exec("docker", ["start", options.container])
  distributorStopped = false
  await waitForHealthyContainer(options.container)
  events.push({ name: "mediamtx-restarted", observedAt: elapsed(startedAt) })
  events.push({
    name: "platform-reload-requested",
    observedAt: elapsed(startedAt),
  })
  await page.reload({ waitUntil: "domcontentloaded", timeout: 60_000 })
  await waitForText(page, "Distribution path: MediaMTX", 120_000)
  await waitForVideo(page, 30_000)
  const recovered = await observeSustainedPlayback(page, {
    durationMilliseconds: 10_000,
    label: "Distribution path: MediaMTX",
  })
  events.push({
    decodedFrames: recovered.observedFrames,
    framesPerSecond: recovered.framesPerSecond,
    height: recovered.height,
    longestStallMilliseconds: recovered.longestStallMilliseconds,
    name: "mediamtx-recovered",
    observedAt: elapsed(startedAt),
    width: recovered.width,
  })
  await drainBrowserEvents(page, browserEvents)
  const fallbackEvents = browserEvents.filter(
    (event) => event.name === "rstream:video-distributor-fallback",
  )
  if (
    !fallbackEvents.some(
      (event) =>
        event.detail?.from === "mediamtx" && event.detail?.to === "direct",
    )
  ) {
    throw new Error(
      "the browser did not report the MediaMTX-to-direct fallback",
    )
  }
  events.push({
    name: "browser-close-requested",
    observedAt: elapsed(startedAt),
  })
  await page.close()
  unexpectedDiagnostics = unexpectedBrowserDiagnostics(diagnostics)
  if (unexpectedDiagnostics.length > 0) {
    throw new Error(
      `the browser reported unexpected diagnostics: ${JSON.stringify(unexpectedDiagnostics)}`,
    )
  }
  await writeResult(options.output, {
    browserEvents,
    diagnostics,
    events,
    passed: true,
    platform: safeOrigin(options.platform),
    unexpectedDiagnostics,
    version: 1,
  })
} catch (error) {
  await drainBrowserEvents(page, browserEvents)
  unexpectedDiagnostics = unexpectedBrowserDiagnostics(diagnostics)
  await writeResult(options.output, {
    browserEvents,
    diagnostics,
    error:
      error instanceof Error
        ? sanitize(error.message)
        : sanitize(String(error)),
    events,
    passed: false,
    platform: safeOrigin(options.platform),
    unexpectedDiagnostics,
    version: 1,
  })
  throw error
} finally {
  if (distributorStopped) {
    await exec("docker", ["start", options.container]).catch(() => {})
  }
  await browser?.close().catch(() => {})
}

function parseArguments(args) {
  const values = new Map()
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index]
    const value = args[index + 1]
    if (!name?.startsWith("--") || !value || values.has(name)) {
      throw new Error(
        "usage: browser.mjs --platform URL --container NAME --browser-executable PATH --output PATH",
      )
    }
    values.set(name, value)
  }
  const option = (name) => {
    const value = values.get(name)
    if (!value) {
      throw new Error(`${name} is required`)
    }
    return value
  }
  return {
    browserExecutable: option("--browser-executable"),
    container: option("--container"),
    output: option("--output"),
    platform: new URL(option("--platform")).toString(),
    sessionToken: requiredEnvironment("RSTREAM_QUALIFICATION_SESSION_TOKEN"),
  }
}

function requiredEnvironment(name) {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value
}

async function waitForText(page, text, timeout) {
  await page.getByText(text, { exact: true }).waitFor({
    state: "visible",
    timeout,
  })
}

async function waitForVideo(page, timeout) {
  await page.locator("video").waitFor({ state: "attached", timeout })
  await page.locator("video").evaluate(
    (video, maximumWait) =>
      new Promise((resolve, reject) => {
        if (
          video.readyState >= 2 &&
          video.videoWidth > 0 &&
          video.videoHeight > 0
        ) {
          resolve()
          return
        }
        const timer = window.setTimeout(() => {
          cleanup()
          reject(new Error("video did not become ready"))
        }, maximumWait)
        const ready = () => {
          if (
            video.readyState >= 2 &&
            video.videoWidth > 0 &&
            video.videoHeight > 0
          ) {
            cleanup()
            resolve()
          }
        }
        const cleanup = () => {
          window.clearTimeout(timer)
          video.removeEventListener("loadeddata", ready)
          video.removeEventListener("resize", ready)
        }
        video.addEventListener("loadeddata", ready)
        video.addEventListener("resize", ready)
      }),
    timeout,
  )
}

async function observeSustainedPlayback(
  page,
  {
    durationMilliseconds,
    label,
    maximumStallMilliseconds = 1_500,
    minimumFramesPerSecond = 20,
  },
) {
  const baseline = await videoState(page)
  const initialDecodedFrames = baseline.decodedFrames
  const startedAt = Date.now()
  const deadline = startedAt + durationMilliseconds
  let lastProgressAt = startedAt
  let longestStallMilliseconds = 0
  let previousDecodedFrames = initialDecodedFrames
  let current = baseline
  while (Date.now() < deadline) {
    current = await videoState(page)
    if (!(await page.getByText(label, { exact: true }).isVisible())) {
      throw new Error(
        `playback left the expected path before the gate completed: ${label}`,
      )
    }
    if (current.readyState < 2 || current.width <= 0 || current.height <= 0) {
      throw new Error(`video became unavailable while observing ${label}`)
    }
    if (current.decodedFrames > previousDecodedFrames) {
      const stall = Date.now() - lastProgressAt
      longestStallMilliseconds = Math.max(longestStallMilliseconds, stall)
      lastProgressAt = Date.now()
      previousDecodedFrames = current.decodedFrames
    } else if (Date.now() - lastProgressAt > maximumStallMilliseconds) {
      throw new Error(
        `video stalled for more than ${maximumStallMilliseconds} ms while observing ${label}`,
      )
    }
    await page.waitForTimeout(250)
  }
  const elapsedMilliseconds = Date.now() - startedAt
  const observedFrames = current.decodedFrames - initialDecodedFrames
  const framesPerSecond = (observedFrames * 1_000) / elapsedMilliseconds
  if (framesPerSecond < minimumFramesPerSecond) {
    throw new Error(
      `decoded video averaged ${framesPerSecond.toFixed(1)} fps while observing ${label}; expected at least ${minimumFramesPerSecond}`,
    )
  }
  return {
    ...current,
    framesPerSecond,
    longestStallMilliseconds,
    observedFrames,
  }
}

function videoState(page) {
  return page.locator("video").evaluate((video) => ({
    decodedFrames:
      video.getVideoPlaybackQuality?.().totalVideoFrames ??
      video.webkitDecodedFrameCount ??
      0,
    height: video.videoHeight,
    readyState: video.readyState,
    width: video.videoWidth,
  }))
}

async function waitForHealthyContainer(container) {
  const deadline = Date.now() + 45_000
  let lastStatus = "unknown"
  while (Date.now() < deadline) {
    const result = await exec("docker", [
      "inspect",
      "--format",
      "{{.State.Health.Status}}",
      container,
    ])
    lastStatus = result.stdout.trim()
    if (lastStatus === "healthy") {
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error(`MediaMTX did not recover a healthy state: ${lastStatus}`)
}

async function writeResult(path, result) {
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 })
}

function elapsed(start) {
  return Date.now() - start
}

function currentPhase(timeline) {
  return timeline.at(-1)?.name ?? "startup"
}

function safeOrigin(value) {
  const url = new URL(value)
  return `${url.protocol}//${url.host}`
}

function sanitize(value) {
  return value.replaceAll(/([?&]rstream\.token=)[^&#\s]+/g, "$1[redacted]")
}
