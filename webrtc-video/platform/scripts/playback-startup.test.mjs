import assert from "node:assert/strict"
import test from "node:test"
import vm from "node:vm"
import { installPlaybackStartupTiming } from "./playback-startup.mjs"

function fixture({ supported = true } = {}) {
  let now = 0
  let video = null
  let frame
  let mutation
  let canceled = 0
  let disconnected = 0
  const listeners = new Map()
  const pending = []
  const element = supported
    ? {
        requestVideoFrameCallback(fn) {
          frame = fn
          return 1
        },
        cancelVideoFrameCallback() {
          canceled++
        },
      }
    : {}
  class Peer {
    connectionState = "new"
    listeners = new Map()
    addEventListener(kind, fn) {
      this.listeners.set(kind, fn)
    }
    removeEventListener(kind, fn) {
      if (this.listeners.get(kind) === fn) this.listeners.delete(kind)
    }
    connect() {
      this.connectionState = "connected"
      this.listeners.get("connectionstatechange")?.()
    }
  }
  const nativeFetch = (...args) =>
    new Promise((resolve, reject) => pending.push({ args, resolve, reject }))
  const context = {
    window: {
      fetch: nativeFetch,
      RTCPeerConnection: Peer,
      addEventListener(kind, fn) {
        listeners.set(kind, fn)
      },
      removeEventListener(kind, fn) {
        if (listeners.get(kind) === fn) listeners.delete(kind)
      },
    },
    document: {
      querySelector: (selector) => {
        assert.equal(selector, ".video-player-picture > video")
        return video
      },
    },
    location: { href: "https://app.test/", origin: "https://app.test" },
    URL,
    performance: { now: () => now },
    MutationObserver: class {
      constructor(fn) {
        mutation = fn
      }
      observe() {}
      disconnect() {
        disconnected++
      }
    },
  }
  const install = () =>
    vm.runInNewContext(
      `(${installPlaybackStartupTiming.toString()})()`,
      context,
    )
  install()
  return {
    window: context.window,
    pending,
    install,
    nativeFetch,
    Peer,
    clock(at) {
      now = at
    },
    attach() {
      video = element
      mutation()
    },
    frame(at, fields = {}) {
      now = at
      frame?.(at, {
        expectedDisplayTime: at + 5,
        width: 1280,
        height: 720,
        presentedFrames: 1,
        ...fields,
      })
    },
    snapshot: () => context.window.__playbackStartup.snapshot(),
    stop: () => context.window.__playbackStartup.stop(),
    canceled: () => canceled,
    disconnected: () => disconnected,
    pagehide: () => listeners.get("pagehide")?.(),
    async request(kind, at, headersAt, bodyAt, status) {
      now = at
      const promise = context.window.fetch(
        kind === "auth"
          ? "/api/devices/example/viewer"
          : "https://edge.test/source/whep?secret=never-retain",
        { method: "POST" },
      )
      const payload = { secret: "never-retain" }
      const response = {
        status: status ?? (kind === "auth" ? 200 : 201),
        async json() {
          return payload
        },
      }
      now = headersAt
      pending.shift().resolve(response)
      assert.equal(
        await promise,
        response,
        "Probe must preserve the response object",
      )
      if (kind === "auth") {
        now = bodyAt
        assert.equal(
          await response.json(),
          payload,
          "Probe must preserve the parsed body",
        )
      }
    },
  }
}

async function connect(f) {
  f.attach()
  await f.request("auth", 100, 140, 150)
  f.clock(160)
  const peer = new f.window.RTCPeerConnection()
  await f.request("whep", 180, 220)
  f.clock(250)
  peer.connect()
  return peer
}

test("separates navigation, authorization and first presentation without retaining credentials", async () => {
  const f = fixture()
  await connect(f)
  f.frame(300)
  f.frame(600)
  const result = f.snapshot()
  assert.equal(result.measurementValid, true)
  assert.equal(result.navigationToExpectedDisplayMilliseconds, 305)
  assert.equal(result.authorizationToExpectedDisplayMilliseconds, 205)
  assert.equal(result.authorizationMilliseconds, 50)
  assert.equal(result.peers[0].connectedAt, 250)
  assert.ok(!JSON.stringify(result).includes("never-retain"))
  assert.ok(!JSON.stringify(result).includes("edge.test"))
})

test("late or missing frame evidence cannot establish first presentation", async () => {
  for (const fields of [
    { presentedFrames: 2 },
    { presentedFrames: 0 },
    { expectedDisplayTime: undefined },
    { expectedDisplayTime: 200 },
    { width: 0 },
  ]) {
    const f = fixture()
    await connect(f)
    f.frame(300, fields)
    assert.equal(f.snapshot().measurementValid, false)
    assert.equal(f.snapshot().navigationToExpectedDisplayMilliseconds, null)
  }
  const f = fixture({ supported: false })
  await connect(f)
  assert.equal(f.snapshot().supported, false)
  assert.equal(f.snapshot().measurementValid, false)
})

test("cancel, pagehide and reinstall restore observers and reject late callbacks", async () => {
  const f = fixture()
  const peer = await connect(f)
  f.pagehide()
  f.stop()
  f.frame(300)
  assert.equal(f.canceled(), 1)
  assert.equal(f.snapshot().measurementValid, false)
  assert.equal(f.window.fetch, f.nativeFetch)
  assert.equal(f.window.RTCPeerConnection, f.Peer)
  assert.equal(peer.listeners.size, 0)
  assert.ok(f.disconnected() >= 1)
  f.install()
  await connect(f)
  f.frame(350)
  assert.equal(f.snapshot().measurementValid, true)
  f.install()
  assert.equal(f.snapshot().firstFrame, null)
})

test("failed authorization, repeated attempts or missing body completion invalidate the measurement", async () => {
  for (const failure of ["failed", "retry", "body"]) {
    const f = fixture()
    await connect(f)
    if (failure === "retry") await f.request("auth", 260, 270, 280)
    else if (failure === "failed") f.snapshot().authorization[0].status = 403
    else delete f.snapshot().authorization[0].bodyReadAt
    f.frame(300)
    assert.equal(f.snapshot().measurementValid, false)
  }
})

test("unrelated requests are passed through and fetch rejection is preserved", async () => {
  const f = fixture()
  const failure = new Error("transport failed")
  const input = new URL("https://app.test/api/devices")
  const promise = f.window.fetch(input)
  assert.equal(f.pending[0].args[0], input)
  f.pending.shift().reject(failure)
  await assert.rejects(promise, (error) => error === failure)
  assert.equal(f.snapshot().authorization.length, 0)
  assert.equal(f.snapshot().whep.length, 0)
  const tracked = f.window.fetch("/api/devices/id/viewer", { method: "POST" })
  f.stop()
  f.pending.shift().resolve({ status: 200 })
  await tracked
  assert.equal(f.snapshot().authorization[0].headersAt, null)
})
