import { execFile } from "node:child_process"
import { mkdir, writeFile } from "node:fs/promises"
import { dirname } from "node:path"
import process from "node:process"
import { promisify } from "node:util"

import { chromium } from "playwright-core"
import { qualifyQualityControls } from "./quality.mjs"
import { qualifyRecording } from "./recording.mjs"

import {
  drainBrowserEvents,
  unexpectedBrowserDiagnostics,
} from "./evidence.mjs"

const exec = promisify(execFile)
const options = parseArguments(process.argv.slice(2))
const startedAt = Date.now()
const events = []
const diagnostics = []
const signalingResponses = []
const browserEvents = []
const requiredMediaMTX = process.env.MEDIAMTX_ALLOW_DIRECT_FALLBACK === "false"
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
  context.setDefaultTimeout(30000)
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
    const originalFetch = window.fetch.bind(window)
    window.fetch = (input, init) => {
      const url = input instanceof Request ? input.url : String(input)
      if (/\/api\/devices\/[^/]+\/quality$/.test(url)) {
        const started = performance.now()
        init?.signal?.addEventListener(
          "abort",
          () => {
            window.__rstreamQualificationEvents.push({
              name: "quality-request-aborted",
              at: Date.now(),
              url: new URL(url, window.location.href).href,
              method: init.method,
              started,
              observedAt: performance.now(),
              reason: String(init.signal.reason),
            })
          },
          { once: true },
        )
      }
      return originalFetch(input, init).then((response) => {
        if (
          /\/api\/devices\/[^/]+\/(quality|recordings)$/.test(url) &&
          response.status === 200
        ) {
          void response
            .clone()
            .json()
            .then((body) => {
              if (init?.signal?.aborted) return
              if (
                /\/quality$/.test(url) &&
                Array.isArray(body.modes) &&
                typeof body.version === "string" &&
                body.modes.some((mode) => mode.id === body.selected)
              ) {
                window.__rstreamQualificationEvents.push({
                  name: "quality-response-read",
                  at: Date.now(),
                  url: new URL(url, window.location.href).href,
                  method: init?.method ?? "GET",
                  status: response.status,
                })
              } else if (
                /\/recordings$/.test(url) &&
                Number.isFinite(Date.parse(body.windowStart)) &&
                Number.isFinite(Date.parse(body.windowEnd)) &&
                Number.isFinite(body.maximumClipSeconds) &&
                body.maximumClipSeconds > 0 &&
                body.maximumClipSeconds <= 30 &&
                Array.isArray(body.spans) &&
                body.spans.length <= 256 &&
                body.spans.every(
                  (span) =>
                    Number.isFinite(Date.parse(span.start)) &&
                    Date.parse(span.end) > Date.parse(span.start),
                )
              ) {
                window.__rstreamQualificationEvents.push({
                  name: "recording-index-response-read",
                  at: Date.now(),
                  url: new URL(url, window.location.href).href,
                  method: init?.method ?? "GET",
                  status: response.status,
                })
              }
            })
            .catch(() => {})
        }
        return response
      })
    }
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
    const request = response.request()
    if (
      isWHEPSignalingRequest(request) ||
      /\/api\/devices\/[^/]+\/(quality|metrics|recordings)$/.test(
        new URL(request.url()).pathname,
      )
    ) {
      signalingResponses.push({
        method: request.method(),
        observedAt: elapsed(startedAt),
        status: response.status(),
        url: sanitize(response.url()),
      })
    }
    if (response.status() >= 400) {
      diagnostics.push({
        message: sanitize(
          `${request.method()} ${response.url()} ${response.status()}`,
        ),
        observedAt: elapsed(startedAt),
        phase: currentPhase(events),
        type: "http-error",
      })
    }
  })
  page.on("requestfailed", (request) => {
    diagnostics.push({
      at: Date.now(),
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
  if (process.env.RSTREAM_QUALIFICATION_QUALITY === "1") {
    events.push({ name: "quality-started", observedAt: elapsed(startedAt) })
    const quality = await qualifyQualityControls({
      context,
      page,
      platform: options.platform,
      waitForVideo,
      observeSustainedPlayback,
      outputDirectory: dirname(options.output),
    })
    events.push({
      name: "quality-passed",
      observedAt: elapsed(startedAt),
      ...quality,
    })
  } else if (await page.getByLabel("Source quality", { exact: true }).count()) {
    throw new Error("Unconfigured producer exposed quality controls")
  }
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
  await page
    .locator(".distribution-metrics")
    .getByText("Source ready", { exact: true })
    .waitFor({ timeout: 10000 })
  events.at(-1).metricsReady = true
  if (process.env.RSTREAM_QUALIFICATION_RECORDING === "1") {
    const sessionsBefore = signalingResponses.filter(
      (response) =>
        response.method === "POST" && /\/whep(?:[/?]|$)/.test(response.url),
    ).length
    const recording = await qualifyRecording({
      context,
      page,
      platform: options.platform,
      container: options.container,
      waitForVideo,
      outputDirectory: dirname(options.output),
      mark: (name) => events.push({ name, observedAt: elapsed(startedAt) }),
    })
    const sessionsAfter = signalingResponses.filter(
      (response) =>
        response.method === "POST" && /\/whep(?:[/?]|$)/.test(response.url),
    ).length
    if (sessionsAfter !== sessionsBefore)
      throw new Error("Recording fault reconnected the live viewer")
    events.push({
      name: "recording-passed",
      observedAt: elapsed(startedAt),
      ...recording,
    })
  }
  events.push({
    name: "mediamtx-stop-requested",
    observedAt: elapsed(startedAt),
  })
  await exec("docker", ["stop", "--timeout", "10", options.container])
  distributorStopped = true
  events.push({ name: "mediamtx-stopped", observedAt: elapsed(startedAt) })
  await page.waitForFunction(
    () =>
      document.body.innerText.includes("Distribution metrics unavailable.") ||
      document.body.innerText.includes(
        "Distribution path: Direct (MediaMTX fallback)",
      ),
    null,
    { timeout: 15000, polling: 250 },
  )
  if (await page.locator(".distribution-metrics").count())
    throw new Error("Unavailable metrics retained old measured values")
  events.at(-1).metricsUnavailableHandled = true
  if (requiredMediaMTX) {
    const deadline = Date.now() + 15_000
    while (Date.now() < deadline) {
      if (
        await page
          .getByText("Distribution path: Direct", { exact: false })
          .count()
      )
        throw new Error("Required MediaMTX switched to direct playback")
      await page.waitForTimeout(250)
    }
    const inventory = await context.request.get(
      new URL("/api/devices", options.platform).href,
    )
    const { devices } = await inventory.json()
    const forcedDirect = await context.request.post(
      new URL(
        `/api/devices/${devices[0].id}/viewer?distribution=direct`,
        options.platform,
      ).href,
      { headers: { Origin: new URL(options.platform).origin } },
    )
    if (forcedDirect.status() !== 403)
      throw new Error(
        "Required MediaMTX accepted explicit direct authorization",
      )
  } else {
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
    if (process.env.RSTREAM_QUALIFICATION_SOURCE_FORMATS === "1") {
      const quality = await qualifyQualityControls({
        context,
        page,
        platform: options.platform,
        waitForVideo,
        observeSustainedPlayback,
        outputDirectory: dirname(options.output),
        direct: true,
      })
      events.push({
        name: "direct-source-formats-passed",
        observedAt: elapsed(startedAt),
        ...quality,
      })
    }
  }
  await drainBrowserEvents(page, browserEvents)
  await exec("docker", ["start", options.container])
  distributorStopped = false
  await waitForHealthyContainer(options.container)
  events.push({
    name: "mediamtx-restarted",
    observedAt: elapsed(startedAt),
    requiredMediaMTXEnforced: requiredMediaMTX,
  })
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
  await page
    .locator(".distribution-metrics")
    .getByText("Source ready", { exact: true })
    .waitFor({ timeout: 10000 })
  events.at(-1).metricsRecovered = true
  await drainBrowserEvents(page, browserEvents)
  const fallbackEvents = browserEvents.filter(
    (event) => event.name === "rstream:video-distributor-fallback",
  )
  if (
    !requiredMediaMTX &&
    !fallbackEvents.some(
      (event) =>
        event.detail?.from === "mediamtx" && event.detail?.to === "direct",
    )
  ) {
    throw new Error(
      "the browser did not report the MediaMTX-to-direct fallback",
    )
  }
  if (requiredMediaMTX && fallbackEvents.length !== 0)
    throw new Error("Required MediaMTX emitted a direct fallback")
  events.push({
    name: "browser-close-requested",
    observedAt: elapsed(startedAt),
  })
  await page.close()
  unexpectedDiagnostics = unexpectedBrowserDiagnostics(
    diagnostics,
    signalingResponses,
    browserEvents,
  )
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
    signalingResponses,
    unexpectedDiagnostics,
    version: 1,
  })
} catch (error) {
  await drainBrowserEvents(page, browserEvents)
  unexpectedDiagnostics = unexpectedBrowserDiagnostics(
    diagnostics,
    signalingResponses,
    browserEvents,
  )
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
    signalingResponses,
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

function isWHEPSignalingRequest(request) {
  return (
    new Set(["POST", "PATCH", "DELETE"]).has(request.method()) &&
    /\/whep(?:[/?]|$)/.test(new URL(request.url()).pathname)
  )
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
