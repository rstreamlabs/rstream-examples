import assert from "node:assert/strict"
import { createSocket } from "node:dgram"
import { mkdir } from "node:fs/promises"
import { join } from "node:path"

// Local, real WebRTC media exercises layout continuity independently of the
// external engine. Transport/network qualification remains a separate harness.
export async function qualifyFullPage({
  context,
  origin,
  directory,
  name,
  observeDistribution = false,
}) {
  // A real local STUN responder keeps ICE gathering deterministic without
  // depending on a public service or waiting for a deliberately dead URI.
  const stun = createSocket("udp4")
  stun.on("message", (packet, remote) => {
    if (
      packet.length < 20 ||
      packet.readUInt16BE(0) !== 1 ||
      packet.readUInt32BE(4) !== 0x2112a442
    )
      return
    const response = Buffer.alloc(32)
    response.writeUInt16BE(0x0101, 0)
    response.writeUInt16BE(12, 2)
    packet.copy(response, 4, 4, 20)
    response.writeUInt16BE(0x0020, 20)
    response.writeUInt16BE(8, 22)
    response[25] = 1
    response.writeUInt16BE(remote.port ^ 0x2112, 26)
    const address = remote.address.split(".").map(Number)
    for (let i = 0; i < 4; i++) response[28 + i] = address[i] ^ response[4 + i]
    stun.send(response, remote.port, remote.address)
  })
  await new Promise((resolve, reject) => {
    stun.once("error", reject)
    stun.bind(0, "127.0.0.1", resolve)
  })
  const stunURL = `stun:127.0.0.1:${stun.address().port}`
  const page = await context.newPage()
  const errors = []
  const signaling = []
  page.on("pageerror", (error) => errors.push(error.message))
  let sessions = 0
  let metricsAvailable = true
  if (observeDistribution)
    await page.route("**/api/devices/*/metrics", (route) => {
      if (!metricsAvailable)
        return route.fulfill({
          status: 503,
          json: { error: "Distribution metrics are unavailable" },
        })
      return route.fulfill({
        json: {
          state: "ready",
          readers: 2,
          inboundBitsPerSecond: 920000,
          outboundBitsPerSecond: 1840000,
          sampledAt: new Date().toISOString(),
          intervalMs: 5000,
        },
      })
    })
  await page.route("**/api/devices/*/viewer", (route) =>
    route.fulfill({
      json: {
        allowDirectFallback: false,
        distributor: {
          kind: observeDistribution ? "mediamtx" : "direct",
          whep: `${origin}/__qualification/whep`,
          authorization: observeDistribution
            ? "Bearer local-synthetic-source"
            : "",
          expiresAt: new Date(Date.now() + 300000).toISOString(),
        },
        turn: {
          username: "",
          credential: "",
          urls: [stunURL],
          ttl: 300,
          expiresAt: new Date(Date.now() + 300000).toISOString(),
        },
      },
    }),
  )
  await page.route("**/__qualification/whep**", async (route) => {
    try {
      const request = route.request()
      signaling.push({
        method: request.method(),
        candidates: request.postData()?.match(/a=candidate:/g)?.length ?? 0,
      })
      if (request.method() === "POST") {
        sessions++
        const answer = await page.evaluate(
          async ({ sdp, stunURL }) => {
            const previous = window.__fullPageSource
            if (previous) {
              clearInterval(previous.timer)
              previous.peer.close()
              for (const track of previous.stream.getTracks()) track.stop()
            }
            const canvas = document.createElement("canvas")
            canvas.width = 1280
            canvas.height = 720
            const drawing = canvas.getContext("2d")
            const colors = [
              "#eee",
              "#e8d54a",
              "#4dc5c7",
              "#49b361",
              "#b64eb7",
              "#d94d4d",
              "#4163b4",
            ]
            let frame = 0
            const draw = () => {
              colors.forEach((color, index) => {
                drawing.fillStyle = color
                drawing.fillRect((index * 1280) / 7, 0, 1280 / 7 + 1, 720)
              })
              drawing.fillStyle = "#171512"
              drawing.fillRect(0, 550, 1280, 170)
              drawing.fillStyle = "#f5f2e9"
              drawing.font = "28px sans-serif"
              drawing.fillText("LIVE VIDEO · TEST SOURCE", 40, 610)
              drawing.font = "20px monospace"
              drawing.fillText(
                `FRAME ${String(frame++).padStart(6, "0")}   1280 × 720`,
                40,
                660,
              )
              drawing.fillRect(40 + (frame % 120) * 10, 692, 20, 5)
            }
            draw()
            const timer = setInterval(draw, 1000 / 30)
            const stream = canvas.captureStream(30)
            const peer = new RTCPeerConnection({
              bundlePolicy: "max-bundle",
              iceServers: [{ urls: stunURL }],
            })
            for (const track of stream.getTracks()) peer.addTrack(track, stream)
            window.__fullPageSource = { peer, stream, timer }
            await peer.setRemoteDescription({ type: "offer", sdp })
            await peer.setLocalDescription(await peer.createAnswer())
            await new Promise((resolve, reject) => {
              const done = () => {
                // One local host candidate is sufficient for this same-machine
                // fixture. WebKit can keep gathering until connectivity starts.
                if (!peer.localDescription.sdp.includes("\na=candidate:"))
                  return
                clearTimeout(timeout)
                peer.removeEventListener("icegatheringstatechange", done)
                peer.removeEventListener("icecandidate", done)
                resolve()
              }
              const timeout = setTimeout(() => {
                peer.removeEventListener("icegatheringstatechange", done)
                peer.removeEventListener("icecandidate", done)
                reject(new Error("Local source ICE candidate deadline"))
              }, 5000)
              peer.addEventListener("icegatheringstatechange", done)
              peer.addEventListener("icecandidate", done)
              done()
            })
            return peer.localDescription.sdp
          },
          { sdp: request.postData(), stunURL },
        )
        await route.fulfill({
          status: 201,
          headers: {
            "Content-Type": "application/sdp",
            Location: "/__qualification/whep/session",
            ETag: '"local-source"',
          },
          body: answer,
        })
      } else if (request.method() === "PATCH") {
        await page.evaluate(async (fragment) => {
          let mid = "0"
          for (const line of fragment.split(/\r?\n/)) {
            if (line.startsWith("a=mid:")) mid = line.slice(6)
            if (line.startsWith("a=candidate:"))
              await window.__fullPageSource.peer.addIceCandidate({
                candidate: line.slice(2),
                sdpMid: mid,
              })
            else if (line === "a=end-of-candidates")
              await window.__fullPageSource.peer.addIceCandidate({
                candidate: "",
                sdpMid: mid,
              })
          }
        }, request.postData())
        await route.fulfill({ status: 204 })
      } else {
        await route.fulfill({ status: 200 })
      }
    } catch (error) {
      // Async route callbacks are event handlers: contain rejection here so
      // the outer integration harness can report failure and release fixtures.
      errors.push(`Local source signaling: ${error.message}`)
      await route
        .fulfill({ status: 503, body: "Local source unavailable" })
        .catch(() => {})
    }
  })
  const capture = async (suffix) => {
    if (!directory) return
    await mkdir(directory, { recursive: true })
    await page.screenshot({
      path: join(directory, `${name}-${suffix}.png`),
      fullPage: suffix.startsWith("inline") || suffix === "metrics-unavailable",
    })
  }
  try {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(origin)
    await page.waitForFunction(
      () => {
        const video = document.querySelector("video")
        return (
          video &&
          video.readyState >= 2 &&
          !video.paused &&
          video.currentTime > 2
        )
      },
      null,
      { timeout: 30000 },
    )
    const video = await page.locator("video").elementHandle()
    const source = await video.evaluateHandle((element) => element.srcObject)
    const before = await video.evaluate((element) => element.currentTime)
    const initialSessions = sessions
    const initialTabs = context.pages().length
    const initialURL = page.url()
    const assertControls = async () => {
      const select = await page
        .getByRole("combobox", { name: "Source quality" })
        .boundingBox()
      const button = page.locator(".video-player-expand")
      const icon = await button.boundingBox()
      const arrow = await page.locator(".source-quality svg").boundingBox()
      const picture = await page.locator(".video-player-picture").boundingBox()
      assert.ok(
        select.y >= picture.y + picture.height,
        "Controls belong below the video",
      )
      assert.ok(
        Math.abs(select.y + select.height / 2 - icon.y - icon.height / 2) < 1,
        "Controls share one line",
      )
      assert.ok(
        Math.abs(select.y + select.height / 2 - arrow.y - arrow.height / 2) < 1,
        "Select chevron is vertically centered",
      )
      assert.ok(
        Math.abs(select.x + select.width - arrow.x - arrow.width - 12) < 1,
        "Select chevron retains 12px trailing space",
      )
      assert.equal(
        (await button.innerText()).trim(),
        "",
        "Expansion control uses only its accessible icon",
      )
    }
    await assertControls()
    if (observeDistribution) {
      await page.getByText("Source: 0.9 Mbit/s", { exact: true }).waitFor()
      assert.match(
        await page.locator(".distribution-metrics").innerText(),
        /2 readers/,
      )
      assert.match(
        await page.locator(".distribution-metrics").innerText(),
        /To readers: 1.8 Mbit\/s/,
      )
    }
    await capture("inline")
    if (observeDistribution) {
      metricsAvailable = false
      await page
        .getByText("Distribution metrics unavailable.", { exact: true })
        .waitFor({ timeout: 10000 })
      assert.equal(
        await page.locator(".distribution-metrics").count(),
        0,
        "Never display stale metrics as current",
      )
      await capture("metrics-unavailable")
      metricsAvailable = true
      await page.locator(".distribution-metrics").waitFor({ timeout: 10000 })
    }
    const assertExpanded = async () => {
      const dialog = page.getByRole("dialog")
      await dialog.waitFor()
      const box = await dialog.boundingBox()
      const viewport = page.viewportSize()
      assert.ok(Math.abs(box.x) < 1 && Math.abs(box.y) < 1)
      assert.ok(Math.abs(box.width - viewport.width) < 1)
      assert.ok(Math.abs(box.height - viewport.height) < 1)
      assert.equal(
        await video.evaluate(
          (element) => element === document.querySelector("video"),
        ),
        true,
      )
      assert.equal(
        await video.evaluate(
          (element, stream) => element.srcObject === stream,
          source,
        ),
        true,
      )
      assert.equal(
        await page.evaluate(() => document.querySelector("header").inert),
        true,
      )
      const exit = await page
        .getByRole("button", { name: "Exit full page", exact: true })
        .boundingBox()
      assert.ok(
        exit.y >= 0 &&
          exit.x >= 0 &&
          exit.x + exit.width <= viewport.width &&
          exit.y + exit.height <= viewport.height,
      )
      assert.equal(
        await dialog.evaluate(
          (element) => element.scrollWidth <= element.clientWidth,
        ),
        true,
      )
      await assertControls()
      if (observeDistribution)
        assert.equal(
          await page.locator(".distribution-metrics").count(),
          0,
          "Full-page controls stay compact",
        )
    }
    await page.evaluate(() => window.scrollTo(0, 160))
    const scrollBefore = await page.evaluate(() => window.scrollY)
    await page.getByRole("button", { name: "Full page", exact: true }).click()
    await assertExpanded()
    await capture("full-page-desktop")
    await page.keyboard.press("Tab")
    assert.equal(
      await page.evaluate(() =>
        document
          .querySelector('[role="dialog"]')
          .contains(document.activeElement),
      ),
      true,
    )
    await page.keyboard.press("Shift+Tab")
    await page.keyboard.press("Escape")
    assert.equal(await page.getByRole("dialog").count(), 0)
    assert.equal(
      await page.evaluate(() =>
        document.activeElement?.getAttribute("aria-label"),
      ),
      "Full page",
    )
    assert.equal(await page.evaluate(() => window.scrollY), scrollBefore)
    assert.equal(
      await page.evaluate(() => document.querySelector("header").inert),
      false,
    )
    await page.setViewportSize({ width: 390, height: 844 })
    await assertControls()
    await capture("inline-mobile")
    await page.getByRole("button", { name: "Full page", exact: true }).click()
    await assertExpanded()
    await capture("full-page-mobile")
    await page.setViewportSize({ width: 320, height: 568 })
    await assertExpanded()
    await page.setViewportSize({ width: 844, height: 390 })
    await assertExpanded()
    await capture("full-page-landscape")
    await page
      .getByRole("button", { name: "Exit full page", exact: true })
      .click()
    assert.equal(await page.getByRole("dialog").count(), 0)
    assert.equal(
      sessions,
      initialSessions,
      "Expansion must not renegotiate WebRTC",
    )
    assert.equal(
      context.pages().length,
      initialTabs,
      "Expansion must not create tabs",
    )
    assert.equal(page.url(), initialURL)
    assert.ok(
      await video.evaluate(
        (element, start) => element.currentTime > start,
        before,
      ),
    )
    // Losing the selected source while expanded must restore the page too.
    await page.getByRole("button", { name: "Full page", exact: true }).click()
    await page.route("**/api/devices", (route) =>
      route.fulfill({ json: { devices: [] } }),
    )
    await page.getByRole("dialog").waitFor({ state: "hidden", timeout: 15000 })
    assert.equal(await page.evaluate(() => document.body.style.position), "")
    assert.equal(
      await page.evaluate(() => document.querySelector("header").inert),
      false,
    )
    assert.deepEqual(errors, [])
    console.log(
      `PASS: ${name} full-page playback continuity, same tab, keyboard/focus, scroll restoration, responsive layouts and source loss`,
    )
  } catch (error) {
    await capture("failure")
    console.error(
      name,
      await page.evaluate(() => ({
        text: document.body.innerText,
        source: window.__fullPageSource?.peer.connectionState,
        ice: window.__fullPageSource?.peer.iceConnectionState,
        localCandidates:
          window.__fullPageSource?.peer.localDescription?.sdp.match(
            /a=candidate:/g,
          )?.length ?? 0,
        remoteCandidates:
          window.__fullPageSource?.peer.remoteDescription?.sdp.match(
            /a=candidate:/g,
          )?.length ?? 0,
        video: document.querySelector("video") && {
          ready: document.querySelector("video").readyState,
          time: document.querySelector("video").currentTime,
          paused: document.querySelector("video").paused,
        },
      })),
      { sessions, errors, signaling },
    )
    throw error
  } finally {
    await page
      .evaluate(() => {
        const source = window.__fullPageSource
        if (!source) return
        clearInterval(source.timer)
        source.peer.close()
        for (const track of source.stream.getTracks()) track.stop()
      })
      .catch(() => {})
    await page.close()
    await new Promise((resolve) => stun.close(resolve))
  }
}
