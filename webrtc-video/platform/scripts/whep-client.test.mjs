import assert from "node:assert/strict"
import test from "node:test"

import {
  WHEPClient,
  WHEPHTTPError,
  addRTCPMuxOnly,
  createICEFragment,
  replaceICEGeneration,
  requireETag,
  sessionURL,
} from "../../shared/whep-client.ts"

const initialOffer = sdp({
  direction: "recvonly",
  pwd: "client-pwd-1",
  ufrag: "client-1",
})
const restartOffer = sdp({
  direction: "recvonly",
  pwd: "client-pwd-2",
  ufrag: "client-2",
})
const initialAnswer = sdp({
  candidate: "candidate:server1 1 udp 2130706431 192.0.2.1 5000 typ host",
  direction: "sendonly",
  pwd: "server-pwd-1",
  rtcpMuxOnly: true,
  ufrag: "server-1",
})
const restartAnswerFragment = [
  "a=ice-options:trickle",
  "a=group:BUNDLE 0",
  "m=video 9 UDP/TLS/RTP/SAVPF 96 97",
  "a=mid:0",
  "a=ice-ufrag:server-2",
  "a=ice-pwd:server-pwd-2",
  "a=candidate:server2 1 udp 2130706431 198.51.100.2 6000 typ host",
  "a=end-of-candidates",
  "",
].join("\r\n")

test("WHEP SDP helpers enforce max-bundle fragments and RTCP multiplexing", () => {
  const offer = addRTCPMuxOnly(initialOffer)
  assert.match(offer, /a=rtcp-mux\r\na=rtcp-mux-only\r\n/)
  assert.equal(addRTCPMuxOnly(offer), offer)
  const fragment = createICEFragment(
    initialOffer,
    [
      {
        candidate:
          "candidate:client1 1 udp 2130706431 192.0.2.10 4000 typ host",
        sdpMid: "0",
      },
    ],
    true,
  )
  assert.match(fragment, /^a=ice-options:trickle\r\na=group:BUNDLE 0\r\n/)
  assert.match(fragment, /a=ice-ufrag:client-1\r\na=ice-pwd:client-pwd-1/)
  assert.match(fragment, /a=candidate:client1 /)
  assert.match(fragment, /a=end-of-candidates\r\n$/)
})

test("WHEP ICE restart replaces credentials and the complete candidate set", () => {
  const answer = replaceICEGeneration(initialAnswer, restartAnswerFragment)
  assert.doesNotMatch(answer, /server-1|candidate:server1/)
  assert.match(answer, /a=ice-ufrag:server-2/)
  assert.match(answer, /a=ice-pwd:server-pwd-2/)
  assert.match(answer, /a=candidate:server2 /)
  assert.match(answer, /a=end-of-candidates/)
})

test("WHEP validates strong ETags and session origins", () => {
  assert.equal(requireETag('"generation-1"', false), '"generation-1"')
  for (const value of [null, "", "*", 'W/"weak"', "unquoted"]) {
    assert.throws(() => requireETag(value, false), /strong ETag/)
  }
  assert.equal(requireETag("*", true), "*")
  const endpoint = new URL("https://edge.example/whep?rstream.token=secret")
  const session = sessionURL(endpoint, "/whep/session-1")
  assert.equal(session.pathname, "/whep/session-1")
  assert.equal(session.searchParams.get("rstream.token"), "secret")
  assert.throws(
    () => sessionURL(endpoint, "https://attacker.example/whep/session-1"),
    /changed origin/,
  )
  assert.equal(
    sessionURL(
      endpoint,
      "/whep/session-1?rstream.token=stale",
    ).searchParams.get("rstream.token"),
    "secret",
  )
})

test("WHEP rotates an edge credential without requiring application authorization", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "",
    credentialExpiresAt: new Date(Date.now() + 1).toISOString(),
    endpoint: "https://edge.example/whep?rstream.token=edge-old",
    fetch: async (input, init) => {
      requests.push({
        authorization: header(init.headers, "Authorization"),
        method: init.method,
        url: String(input),
      })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/edge-only-session",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
    refreshCredentials: async () => ({
      authorization: "",
      endpoint: "https://edge.example/whep?rstream.token=edge-new",
      expiresAt: new Date(Date.now() + 120_000).toISOString(),
      iceExpiresAt: new Date(Date.now() + 120_000).toISOString(),
      iceServers: [],
    }),
  })
  await client.start()
  assert.deepEqual(requests[0], {
    authorization: null,
    method: "POST",
    url: "https://edge.example/whep?rstream.token=edge-new",
  })
  await client.close()
  assert.equal(
    requests.at(-1).url,
    "https://edge.example/whep/edge-only-session?rstream.token=edge-new",
  )
  assert.equal(requests.at(-1).authorization, null)
})

test("WHEP client uses one strict Trickle ICE session across an ICE restart", async () => {
  const requests = []
  const errors = []
  const peer = new FakePeer()
  const fakeFetch = async (input, init = {}) => {
    const url = String(input)
    requests.push({
      body: String(init.body ?? ""),
      headers: init.headers ?? {},
      method: init.method,
      url,
    })
    if (init.method === "POST") {
      return response(initialAnswer, 201, {
        "Content-Type": "application/sdp",
        ETag: '"generation-1"',
        Location: "/whep/session-1",
        "X-Rstream-Diagnostics-Token": "diagnostics-token",
      })
    }
    if (init.method === "PATCH" && header(init.headers, "If-Match") === "*") {
      return response(restartAnswerFragment, 200, {
        "Content-Type": "application/trickle-ice-sdpfrag",
        ETag: '"generation-2"',
      })
    }
    if (init.method === "PATCH") {
      return response(null, 204)
    }
    if (init.method === "DELETE") {
      return response(null, 200)
    }
    throw new Error(`unexpected ${init.method} ${url}`)
  }
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep?rstream.token=edge-token",
    fetch: fakeFetch,
    iceServers: [],
    onError: (error) => errors.push(error),
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  assert.equal(
    client.sessionHeader("X-Rstream-Diagnostics-Token"),
    "diagnostics-token",
  )
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  const candidatePatch = requests.find(
    (request) =>
      request.method === "PATCH" && header(request.headers, "If-Match") !== "*",
  )
  assert.equal(
    candidatePatch.url,
    "https://edge.example/whep/session-1?rstream.token=edge-token",
  )
  assert.equal(
    requests[0].url,
    "https://edge.example/whep?rstream.token=edge-token",
  )
  assert.equal(
    header(candidatePatch.headers, "Authorization"),
    "Bearer viewer-token",
  )
  assert.equal(header(candidatePatch.headers, "If-Match"), '"generation-1"')
  assert.match(candidatePatch.body, /candidate:client-1 /)
  assert.match(requests[0].body, /a=rtcp-mux-only/)
  await client.restart()
  const restartPatch = requests.find(
    (request) =>
      request.method === "PATCH" && header(request.headers, "If-Match") === "*",
  )
  assert.match(restartPatch.body, /a=ice-ufrag:client-2/)
  assert.match(restartPatch.body, /candidate:client-2 /)
  assert.match(peer.remoteDescription.sdp, /a=ice-ufrag:server-2/)
  assert.doesNotMatch(peer.remoteDescription.sdp, /candidate:server1/)
  assert.equal(peer.createOfferCalls, 2)
  assert.equal(peer.closed, false)
  await client.close()
  assert.equal(peer.closed, true)
  assert.equal(requests.at(-1).method, "DELETE")
  assert.equal(
    requests.at(-1).url,
    "https://edge.example/whep/session-1?rstream.token=edge-token",
  )
  assert.equal(
    header(requests.at(-1).headers, "Authorization"),
    "Bearer viewer-token",
  )
  assert.deepEqual(errors, [])
})

test("WHEP sends a separate completion fragment when gathering finishes after candidates", async () => {
  const requests = []
  const peer = new ManualCompletionPeer()
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (input, init) => {
      requests.push({ body: String(init.body ?? ""), method: init.method })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/late-completion",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: (error) => assert.fail(error.message),
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  const first = requests.find((request) => request.method === "PATCH")
  assert.match(first.body, /a=candidate:extra-1 /)
  assert.doesNotMatch(first.body, /a=end-of-candidates/)
  peer.completeGathering()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 2,
  )
  const completion = requests.filter((request) => request.method === "PATCH")[1]
  assert.match(completion.body, /a=end-of-candidates\r\n$/)
  assert.doesNotMatch(completion.body, /a=candidate:/)
  await client.close()
})

test("WHEP does not patch a candidate already embedded in its initial offer", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      requests.push({ body: String(init.body ?? ""), method: init.method })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/embedded-candidate",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: (error) => assert.fail(error.message),
    onTrack: () => {},
    peerFactory: () => new EmbeddedCandidatePeer(),
  })
  await client.start()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  const initial = requests.find((request) => request.method === "POST")
  const completion = requests.find((request) => request.method === "PATCH")
  assert.match(initial.body, /a=candidate:client-1 /)
  assert.doesNotMatch(completion.body, /a=candidate:/)
  assert.match(completion.body, /a=end-of-candidates\r\n$/)
  await client.close()
})

test("WHEP renews expiring ICE credentials before a healthy relay allocation expires", async () => {
  const requests = []
  const peer = new FakePeer()
  let refreshes = 0
  const client = new WHEPClient({
    authorization: "Bearer application-old",
    credentialExpiresAt: new Date(Date.now() + 120_000).toISOString(),
    endpoint: "https://edge.example/whep?rstream.token=edge-old",
    fetch: async (input, init) => {
      requests.push({
        authorization: header(init.headers, "Authorization"),
        ifMatch: header(init.headers, "If-Match"),
        method: init.method,
        url: String(input),
      })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/credential-maintenance",
        })
      }
      if (init.method === "PATCH" && header(init.headers, "If-Match") === "*") {
        return response(restartAnswerFragment, 200, {
          "Content-Type": "application/trickle-ice-sdpfrag",
          ETag: '"generation-2"',
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceCredentialExpiresAt: new Date(Date.now() + 10).toISOString(),
    iceServers: [
      { urls: "turn:old.example", username: "old", credential: "old" },
    ],
    onError: () => {},
    onTrack: () => {},
    peerFactory: (configuration) => {
      peer.configuration = configuration
      return peer
    },
    refreshCredentials: async () => {
      refreshes += 1
      return {
        authorization: "Bearer application-new",
        endpoint: "https://edge.example/whep?rstream.token=edge-new",
        expiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceExpiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceServers: [
          {
            urls: "turn:new.example",
            username: "new",
            credential: "new",
          },
        ],
      }
    },
  })
  await client.start()
  await eventually(() =>
    requests.some(
      (request) => request.method === "PATCH" && request.ifMatch === "*",
    ),
  )
  assert.equal(refreshes, 1)
  const restart = requests.find(
    (request) => request.method === "PATCH" && request.ifMatch === "*",
  )
  assert.equal(
    restart.url,
    "https://edge.example/whep/credential-maintenance?rstream.token=edge-new",
  )
  assert.equal(restart.authorization, "Bearer application-new")
  assert.deepEqual(peer.configuration.iceServers, [
    { urls: "turn:new.example", username: "new", credential: "new" },
  ])
  await client.close()
})

test("WHEP retries transient credential renewal without closing a healthy session", async () => {
  const errors = []
  const requests = []
  let refreshes = 0
  const client = new WHEPClient({
    authorization: "Bearer application-old",
    credentialExpiresAt: new Date(Date.now() + 120_000).toISOString(),
    endpoint: "https://edge.example/whep?rstream.token=edge-old",
    fetch: async (input, init) => {
      requests.push({
        ifMatch: header(init.headers, "If-Match"),
        method: init.method,
        url: String(input),
      })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/transient-renewal",
        })
      }
      if (init.method === "PATCH" && header(init.headers, "If-Match") === "*") {
        return response(restartAnswerFragment, 200, {
          "Content-Type": "application/trickle-ice-sdpfrag",
          ETag: '"generation-2"',
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceCredentialExpiresAt: new Date(Date.now() + 2_500).toISOString(),
    iceServers: [
      { urls: "turn:old.example", username: "old", credential: "old" },
    ],
    onError: (error) => errors.push(error),
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
    refreshCredentials: async () => {
      refreshes += 1
      if (refreshes === 1) {
        throw new Error("credential service temporarily unavailable")
      }
      return {
        authorization: "Bearer application-new",
        endpoint: "https://edge.example/whep?rstream.token=edge-new",
        expiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceExpiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceServers: [
          {
            urls: "turn:new.example",
            username: "new",
            credential: "new",
          },
        ],
      }
    },
  })
  await client.start()
  await eventually(
    () =>
      refreshes === 2 &&
      requests.some(
        (request) => request.method === "PATCH" && request.ifMatch === "*",
      ),
    2_500,
  )
  assert.deepEqual(errors, [])
  assert.equal(client.peer.closed, false)
  await client.close()
})

test("WHEP ICE restart renews edge, application, and TURN credentials", async () => {
  const requests = []
  const peer = new FakePeer()
  let refreshes = 0
  const client = new WHEPClient({
    authorization: "Bearer application-old",
    credentialExpiresAt: new Date(Date.now() + 60_000).toISOString(),
    endpoint: "https://edge.example/whep?rstream.token=edge-old",
    fetch: async (input, init) => {
      requests.push({
        headers: init.headers,
        method: init.method,
        url: String(input),
      })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/renewed-session",
        })
      }
      if (init.method === "PATCH" && header(init.headers, "If-Match") === "*") {
        return response(restartAnswerFragment, 200, {
          "Content-Type": "application/trickle-ice-sdpfrag",
          ETag: '"generation-2"',
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [
      { urls: "turn:old.example", username: "old", credential: "old" },
    ],
    onError: () => {},
    onTrack: () => {},
    peerFactory: (configuration) => {
      peer.configuration = configuration
      return peer
    },
    refreshCredentials: async () => {
      refreshes += 1
      return {
        authorization: "Bearer application-new",
        endpoint: "https://edge.example/whep?rstream.token=edge-new",
        expiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceExpiresAt: new Date(Date.now() + 120_000).toISOString(),
        iceServers: [
          {
            urls: "turn:new.example",
            username: "new",
            credential: "new",
          },
        ],
      }
    },
  })
  await client.start()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  await client.restart()
  const restart = requests.find(
    (request) =>
      request.method === "PATCH" && header(request.headers, "If-Match") === "*",
  )
  assert.equal(refreshes, 1)
  assert.equal(
    restart.url,
    "https://edge.example/whep/renewed-session?rstream.token=edge-new",
  )
  assert.equal(
    header(restart.headers, "Authorization"),
    "Bearer application-new",
  )
  assert.deepEqual(peer.configuration.iceServers, [
    {
      urls: "turn:new.example",
      username: "new",
      credential: "new",
    },
  ])
  await client.close()
  assert.equal(refreshes, 1)
  assert.equal(
    requests.at(-1).url,
    "https://edge.example/whep/renewed-session?rstream.token=edge-new",
  )
})

test("WHEP rejects refreshed credentials for another active target", async () => {
  const requests = []
  const peer = new FakePeer()
  const client = new WHEPClient({
    authorization: "Bearer application-old",
    credentialExpiresAt: new Date(Date.now() + 60_000).toISOString(),
    endpoint: "https://edge.example/whep?rstream.token=edge-old",
    fetch: async (input, init) => {
      requests.push({ method: init.method, url: String(input) })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/bound-target",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
    refreshCredentials: async () => ({
      authorization: "Bearer application-new",
      endpoint: "https://other-edge.example/whep?rstream.token=edge-new",
      expiresAt: new Date(Date.now() + 120_000).toISOString(),
      iceExpiresAt: new Date(Date.now() + 120_000).toISOString(),
      iceServers: [],
    }),
  })
  await client.start()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  await assert.rejects(() => client.restart(), /changed the active target/)
  assert.equal(
    requests.filter((request) => request.method === "PATCH").length,
    1,
  )
  assert.equal(peer.createOfferCalls, 1)
  await client.close()
  assert.equal(
    requests.at(-1).url,
    "https://edge.example/whep/bound-target?rstream.token=edge-old",
  )
})

test("WHEP close retries a refresh cancelled with an active restart", async () => {
  const originalNow = Date.now
  let now = originalNow()
  Date.now = () => now
  try {
    const requests = []
    const peer = new FakePeer()
    let refreshes = 0
    const client = new WHEPClient({
      authorization: "Bearer application-old",
      credentialExpiresAt: new Date(now + 60_000).toISOString(),
      endpoint: "https://edge.example/whep?rstream.token=edge-old",
      fetch: async (input, init) => {
        requests.push({
          authorization: header(init.headers, "Authorization"),
          method: init.method,
          url: String(input),
        })
        if (init.method === "POST") {
          return response(initialAnswer, 201, {
            "Content-Type": "application/sdp",
            ETag: '"generation-1"',
            Location: "/whep/refresh-race",
          })
        }
        return response(null, init.method === "PATCH" ? 204 : 200)
      },
      iceServers: [],
      onError: () => {},
      onTrack: () => {},
      peerFactory: () => peer,
      refreshCredentials: async (signal) => {
        refreshes += 1
        if (refreshes === 1) {
          return new Promise((_resolve, reject) => {
            signal.addEventListener("abort", () => reject(signal.reason), {
              once: true,
            })
          })
        }
        return {
          authorization: "Bearer application-new",
          endpoint: "https://edge.example/whep?rstream.token=edge-new",
          expiresAt: new Date(now + 120_000).toISOString(),
          iceExpiresAt: new Date(now + 120_000).toISOString(),
          iceServers: [],
        }
      },
    })
    await client.start()
    now += 40_000
    const restarting = client.restart()
    await eventually(() => refreshes === 1)
    const closeResult = await client.close()
    await assert.rejects(restarting)
    assert.equal(refreshes, 2)
    assert.equal(closeResult.credentialRefreshFailed, false)
    assert.equal(closeResult.outcome, "deleted")
    assert.deepEqual(requests.at(-1), {
      authorization: "Bearer application-new",
      method: "DELETE",
      url: "https://edge.example/whep/refresh-race?rstream.token=edge-new",
    })
  } finally {
    Date.now = originalNow
  }
})

test("WHEP close remains bounded when credential refresh ignores cancellation", async () => {
  const originalNow = Date.now
  let now = originalNow()
  Date.now = () => now
  try {
    const peer = new FakePeer()
    let deletes = 0
    const client = new WHEPClient({
      authorization: "Bearer application-old",
      credentialExpiresAt: new Date(now + 60_000).toISOString(),
      endpoint: "https://edge.example/whep?rstream.token=edge-old",
      fetch: async (_input, init) => {
        if (init.method === "POST") {
          return response(initialAnswer, 201, {
            "Content-Type": "application/sdp",
            ETag: '"generation-1"',
            Location: "/whep/uninterruptible-refresh",
          })
        }
        if (init.method === "DELETE") {
          deletes += 1
        }
        return response(null, init.method === "PATCH" ? 204 : 200)
      },
      iceServers: [],
      onError: () => {},
      onTrack: () => {},
      peerFactory: () => peer,
      refreshCredentials: async () => new Promise(() => {}),
      requestTimeoutMs: 20,
    })
    await client.start()
    now += 40_000
    const result = await client.close()
    assert.equal(result.credentialRefreshFailed, true)
    assert.equal(result.outcome, "timed-out")
    assert.equal(deletes, 0)
    assert.equal(peer.closed, true)
  } finally {
    Date.now = originalNow
  }
})

test("WHEP client rejects redirects before forwarding credentials", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (input, init) => {
      requests.push({ input: String(input), init })
      return response(null, 307, { Location: "https://attacker.example/whep" })
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await assert.rejects(() => client.start(), /untrusted origin/)
  assert.equal(requests.length, 1)
  assert.equal(requests[0].input, "https://edge.example/whep")
  await client.close()
})

test("WHEP client preserves the edge credential across same-origin redirects", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "",
    endpoint: "https://edge.example/whep?rstream.token=edge-token",
    fetch: async (input, init) => {
      requests.push({ method: init.method, url: String(input) })
      if (requests.length === 1) {
        return response(null, 307, { Location: "/redirected/whep" })
      }
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/redirect-session",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await client.start()
  assert.equal(
    requests[1].url,
    "https://edge.example/redirected/whep?rstream.token=edge-token",
  )
  await client.close()
})

test("WHEP client uses the destination edge credential on a trusted cross-origin redirect", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep?rstream.token=edge-source",
    fetch: async (input, init) => {
      requests.push({
        authorization: header(init.headers, "Authorization"),
        method: init.method,
        url: String(input),
      })
      if (requests.length === 1) {
        return response(null, 307, {
          Location:
            "https://region.example/whep?rstream.token=edge-destination",
        })
      }
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/session-1",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
    trustedRedirectOrigins: ["https://region.example"],
  })
  await client.start()
  assert.equal(
    requests[1].url,
    "https://region.example/whep?rstream.token=edge-destination",
  )
  assert.equal(requests[1].authorization, "Bearer viewer-token")
  assert.equal(
    client.sessionResource(),
    "https://region.example/whep/session-1?rstream.token=edge-destination",
  )
  await client.close()
})

test("WHEP client rejects ambiguous edge credentials", () => {
  assert.throws(
    () =>
      new WHEPClient({
        authorization: "",
        endpoint:
          "https://edge.example/whep?rstream.token=one&rstream.token=two",
        iceServers: [],
        onError: () => {},
        onTrack: () => {},
      }),
    /edge credential is invalid/,
  )
})

test("WHEP client bounds stalled signaling requests", async () => {
  let aborted = false
  const peer = new FakePeer()
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) =>
      new Promise((_resolve, reject) => {
        init.signal.addEventListener(
          "abort",
          () => {
            aborted = true
            reject(init.signal.reason)
          },
          { once: true },
        )
      }),
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
    requestTimeoutMs: 20,
  })
  await assert.rejects(() => client.start(), /request timed out/)
  assert.equal(aborted, true)
  await client.close()
  assert.equal(peer.closed, true)
})

test("WHEP client bounds a response body stalled after its headers", async () => {
  let bodyCancelled = false
  const peer = new FakePeer()
  const body = new ReadableStream({
    cancel() {
      bodyCancelled = true
    },
  })
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async () =>
      new Response(body, {
        headers: {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/stalled-body",
        },
        status: 201,
      }),
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
    requestTimeoutMs: 20,
  })
  await assert.rejects(() => client.start(), /response read timed out/)
  assert.equal(bodyCancelled, true)
  await client.close()
  assert.equal(peer.closed, true)
})

test("WHEP client deletes a created resource when negotiation fails", async () => {
  const requests = []
  const peer = new FakePeer()
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (input, init) => {
      requests.push({ input: String(input), method: init.method })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "text/plain",
          ETag: '"generation-1"',
          Location: "/whep/failed-session",
        })
      }
      return response(null, 204)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await assert.rejects(() => client.start(), /text\/plain/)
  assert.equal(peer.closed, true)
  assert.deepEqual(
    requests.map((request) => request.method),
    ["POST", "DELETE"],
  )
  assert.equal(requests[1].input, "https://edge.example/whep/failed-session")
})

test("WHEP close waits for concurrent resource creation and deletes it exactly once", async () => {
  const requests = []
  const closeResults = []
  let releasePOST
  let markPOSTStarted
  const postStarted = new Promise((resolve) => {
    markPOSTStarted = resolve
  })
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (input, init) => {
      requests.push({ input: String(input), method: init.method })
      if (init.method === "POST") {
        markPOSTStarted()
        return new Promise((resolve) => {
          releasePOST = () =>
            resolve(
              response(initialAnswer, 201, {
                "Content-Type": "application/sdp",
                ETag: '"generation-1"',
                Location: "/whep/concurrent-close",
              }),
            )
        })
      }
      return response(null, 204)
    },
    iceServers: [],
    onClose: (result) => closeResults.push(result),
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  const start = client.start()
  await postStarted
  const firstClose = client.close()
  const secondClose = client.close()
  releasePOST()
  await assert.rejects(() => start, /closed/)
  await Promise.all([firstClose, secondClose])
  assert.deepEqual(
    requests.map((request) => request.method),
    ["POST", "DELETE"],
  )
  assert.equal(requests[1].input, "https://edge.example/whep/concurrent-close")
  assert.equal(closeResults.length, 1)
  assert.equal(closeResults[0].outcome, "deleted")
})

test("WHEP client exposes bounded server retry guidance after saturation", async () => {
  const requests = []
  const closeResults = []
  const peer = new FakePeer()
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      requests.push(init.method)
      return response(null, 503, { "Retry-After": "7" })
    },
    iceServers: [],
    onClose: (result) => closeResults.push(result),
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await assert.rejects(
    () => client.start(),
    (error) => {
      assert.equal(error instanceof WHEPHTTPError, true)
      assert.equal(error.status, 503)
      assert.equal(error.retryAfterMilliseconds, 7000)
      assert.match(error.message, /rejected playback \(503\)/)
      return true
    },
  )
  assert.deepEqual(requests, ["POST"])
  assert.equal(peer.closed, true)
  assert.equal(closeResults.length, 1)
  assert.equal(closeResults[0].outcome, "not-established")
})

test("WHEP retry guidance accepts HTTP dates and rejects ambiguous values", () => {
  const previousNow = Date.now
  Date.now = () => Date.UTC(2026, 7, 18, 3, 0, 0)
  try {
    const date = new WHEPHTTPError(
      "temporarily unavailable",
      response(null, 503, {
        "Retry-After": "Tue, 18 Aug 2026 03:00:09 GMT",
      }),
    )
    assert.equal(date.retryAfterMilliseconds, 9000)
    const obsoleteDate = new WHEPHTTPError(
      "temporarily unavailable",
      response(null, 503, {
        "Retry-After": "Tuesday, 18-Aug-26 03:00:11 GMT",
      }),
    )
    assert.equal(obsoleteDate.retryAfterMilliseconds, 11000)
    const asctimeDate = new WHEPHTTPError(
      "temporarily unavailable",
      response(null, 503, {
        "Retry-After": "Tue Aug 18 03:00:13 2026",
      }),
    )
    assert.equal(asctimeDate.retryAfterMilliseconds, 13000)
    const past = new WHEPHTTPError(
      "temporarily unavailable",
      response(null, 503, {
        "Retry-After": "Tue, 18 Aug 2026 02:59:59 GMT",
      }),
    )
    assert.equal(past.retryAfterMilliseconds, 0)
    for (const value of [
      "",
      "-1",
      "1.5",
      "later",
      "Tue, 31 Feb 2026 03:00:09 GMT",
    ]) {
      const invalid = new WHEPHTTPError(
        "temporarily unavailable",
        response(null, 503, { "Retry-After": value }),
      )
      assert.equal(invalid.retryAfterMilliseconds, null)
    }
    const overflowing = new WHEPHTTPError(
      "temporarily unavailable",
      response(null, 503, {
        "Retry-After": "999999999999999999999999999999999999",
      }),
    )
    assert.equal(overflowing.retryAfterMilliseconds, 2_147_483_647)
  } finally {
    Date.now = previousNow
  }
})

test("WHEP client permits loopback HTTP and omits empty authorization", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "",
    endpoint: "http://127.0.0.1:8080/whep",
    fetch: async (input, init) => {
      requests.push({ input: String(input), init })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/session-1",
        })
      }
      return response(null, 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await client.start()
  assert.equal(header(requests[0].init.headers, "Authorization"), null)
  assert.equal(client.sessionResource(), "http://127.0.0.1:8080/whep/session-1")
  await client.close()
  assert.throws(
    () =>
      new WHEPClient({
        authorization: "",
        endpoint: "http://edge.example/whep",
        iceServers: [],
        onError: () => {},
        onTrack: () => {},
      }),
    /must use HTTPS/,
  )
  assert.throws(
    () =>
      new WHEPClient({
        authorization: "",
        endpoint: "ftp://127.0.0.1/whep",
        iceServers: [],
        onError: () => {},
        onTrack: () => {},
      }),
    /must use HTTPS/,
  )
  const isolated = new WHEPClient({
    allowInsecureHTTP: true,
    authorization: "",
    endpoint: "http://producer.internal:8080/whep",
    fetch: async (input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/session-1",
        })
      }
      return response(null, 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await isolated.start()
  assert.equal(
    isolated.sessionResource(),
    "http://producer.internal:8080/whep/session-1",
  )
  await isolated.close()
})

test("WHEP client completes a standards counter-offer and rejects an expired one", async () => {
  const requests = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (input, init) => {
      requests.push({ body: String(init.body ?? ""), method: init.method })
      if (init.method === "POST") {
        return response(initialAnswer, 406, {
          "Content-Type": `application/sdp; valid-until="${new Date(Date.now() + 60_000).toUTCString()}"`,
          Location: "/whep/counter-offer",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await client.start()
  assert.deepEqual(
    requests.map((request) => request.method),
    ["POST", "PATCH"],
  )
  assert.match(requests[1].body, /a=rtcp-mux-only/)
  await client.close()
  const expired = new WHEPClient({
    authorization: "",
    endpoint: "https://edge.example/whep",
    fetch: async () =>
      response(initialAnswer, 406, {
        "Content-Type": `application/sdp; valid-until="${new Date(Date.now() - 60_000).toUTCString()}"`,
        Location: "/whep/expired-counter-offer",
      }),
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await assert.rejects(() => expired.start(), /counter-offer expired/)
  await expired.close()
  const malformed = new WHEPClient({
    authorization: "",
    endpoint: "https://edge.example/whep",
    fetch: async () =>
      response(initialAnswer, 406, {
        "Content-Type": "application/sdp; valid-until=not-a-date",
        Location: "/whep/malformed-counter-offer",
      }),
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => new FakePeer(),
  })
  await assert.rejects(() => malformed.start(), /valid-until is invalid/)
  await malformed.close()
})

test("WHEP client rejects a counter-offer that expires while it prepares the answer", async () => {
  const originalNow = Date.now
  const receivedAt = originalNow()
  const validUntil = receivedAt + 60_000
  const requests = []
  const peer = new ExpiringCounterOfferPeer(() => {
    Date.now = () => validUntil + 1
  })
  try {
    const client = new WHEPClient({
      authorization: "Bearer viewer-token",
      endpoint: "https://edge.example/whep",
      fetch: async (_input, init) => {
        requests.push(init.method)
        if (init.method === "POST") {
          return response(initialAnswer, 406, {
            "Content-Type": `application/sdp; valid-until="${new Date(validUntil).toUTCString()}"`,
            Location: "/whep/expiring-counter-offer",
          })
        }
        return response(null, 200)
      },
      iceServers: [],
      onError: () => {},
      onTrack: () => {},
      peerFactory: () => peer,
    })
    await assert.rejects(() => client.start(), /counter-offer expired/)
    assert.deepEqual(requests, ["POST", "DELETE"])
  } finally {
    Date.now = originalNow
  }
})

test("WHEP restart rolls back a rejected generation and remains retryable", async () => {
  const peer = new FakePeer()
  let restartAttempts = 0
  const requests = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      requests.push({ headers: init.headers, method: init.method })
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/retryable-restart",
        })
      }
      if (init.method === "PATCH" && header(init.headers, "If-Match") === "*") {
        restartAttempts += 1
        if (restartAttempts === 1) {
          return response(null, 503)
        }
        return response(restartAnswerFragment, 200, {
          "Content-Type": "application/trickle-ice-sdpfrag",
          ETag: '"generation-2"',
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  await eventually(
    () => requests.filter((request) => request.method === "PATCH").length === 1,
  )
  await assert.rejects(() => client.restart(), /failed \(503\)/)
  assert.equal(peer.rollbackCalls, 1)
  assert.equal(peer.signalingState, "stable")
  await Promise.all([client.restart(), client.restart()])
  assert.equal(restartAttempts, 2)
  assert.equal(peer.createOfferCalls, 3)
  assert.match(peer.remoteDescription.sdp, /a=ice-ufrag:server-2/)
  await client.close()
})

test("WHEP close cancels stalled candidate I/O and remains idempotent", async () => {
  const peer = new FakePeer()
  let candidateAborted = false
  let candidateSettled = false
  let deletes = 0
  let peerClosedDuringDelete = null
  let patchStarted = false
  let settleCandidate
  const closeResults = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/stalled-candidate",
        })
      }
      if (init.method === "PATCH") {
        patchStarted = true
        return new Promise((_resolve, reject) => {
          init.signal.addEventListener(
            "abort",
            () => {
              candidateAborted = true
              settleCandidate = () => {
                candidateSettled = true
                reject(init.signal.reason)
              }
            },
            { once: true },
          )
        })
      }
      if (init.method === "DELETE") {
        assert.equal(candidateSettled, true)
        deletes += 1
        peerClosedDuringDelete = peer.closed
        return response(null, 200)
      }
      throw new Error(`unexpected ${init.method}`)
    },
    iceServers: [],
    onClose: (result) => closeResults.push(result),
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  await eventually(() => patchStarted)
  const firstClose = client.close()
  const secondClose = client.close()
  await eventually(() => candidateAborted)
  assert.equal(deletes, 0)
  settleCandidate()
  await Promise.all([firstClose, secondClose])
  assert.equal(deletes, 1)
  assert.equal(peerClosedDuringDelete, false)
  assert.equal(peer.closed, true)
  assert.equal(closeResults.length, 1)
  assert.equal(closeResults[0].outcome, "deleted")
  assert.equal(closeResults[0].status, 200)
  await assert.rejects(() => client.start(), /closed/)
})

test("WHEP close never overlaps deletion with an uninterruptible candidate update", async () => {
  const peer = new FakePeer()
  let deletes = 0
  let patchStarted = false
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/uninterruptible-candidate",
        })
      }
      if (init.method === "PATCH") {
        patchStarted = true
        return new Promise(() => {})
      }
      if (init.method === "DELETE") {
        deletes += 1
        return response(null, 200)
      }
      throw new Error(`unexpected ${init.method}`)
    },
    iceServers: [],
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
    requestTimeoutMs: 20,
  })
  await client.start()
  await eventually(() => patchStarted)
  const result = await client.close()
  assert.equal(result.outcome, "timed-out")
  assert.equal(deletes, 0)
  assert.equal(peer.closed, true)
})

test("WHEP close completes bounded remote deletion before local teardown", async () => {
  const peer = new FakePeer()
  let releaseDelete
  let deleteStarted = false
  const observed = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/ordered-close",
        })
      }
      if (init.method === "DELETE") {
        deleteStarted = true
        return new Promise((resolve) => {
          releaseDelete = () => resolve(response(null, 200))
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onClose: (result) => observed.push({ peerClosed: peer.closed, result }),
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  const closing = client.close()
  await eventually(() => deleteStarted)
  assert.equal(deleteStarted, true)
  assert.equal(peer.closed, false)
  releaseDelete()
  const result = await closing
  assert.equal(peer.closed, true)
  assert.equal(result.outcome, "deleted")
  assert.equal(result.status, 200)
  assert.deepEqual(observed, [{ peerClosed: true, result }])
})

test("WHEP close bounds a stalled deletion with the request budget", async () => {
  const peer = new FakePeer()
  let deletionAborted = false
  const observed = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/stalled-delete",
        })
      }
      if (init.method === "DELETE") {
        return new Promise((_resolve, reject) => {
          init.signal.addEventListener(
            "abort",
            () => {
              deletionAborted = true
              reject(init.signal.reason)
            },
            { once: true },
          )
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onClose: (result) => observed.push(result),
    onError: () => {},
    onTrack: () => {},
    peerFactory: () => peer,
    requestTimeoutMs: 20,
  })
  await client.start()
  const result = await client.close()
  assert.equal(deletionAborted, true)
  assert.equal(peer.closed, true)
  assert.equal(result.outcome, "timed-out")
  assert.deepEqual(observed, [result])
})

test("WHEP close reports rejected and already absent resources", async () => {
  for (const [status, outcome] of [
    [401, "http-error"],
    [404, "already-absent"],
    [410, "already-absent"],
  ]) {
    const peer = new FakePeer()
    const client = new WHEPClient({
      authorization: "Bearer viewer-token",
      endpoint: "https://edge.example/whep",
      fetch: async (_input, init) => {
        if (init.method === "POST") {
          return response(initialAnswer, 201, {
            "Content-Type": "application/sdp",
            ETag: '"generation-1"',
            Location: `/whep/close-${status}`,
          })
        }
        return response(null, init.method === "DELETE" ? status : 204)
      },
      iceServers: [],
      onClose: () => {
        throw new Error("observer failure")
      },
      onError: () => {},
      onTrack: () => {},
      peerFactory: () => peer,
    })
    await client.start()
    const result = await client.close()
    assert.equal(result.outcome, outcome)
    assert.equal(result.status, status)
    assert.equal(peer.closed, true)
  }
})

test("WHEP candidate bounds report one terminal signaling error", async () => {
  const peer = new FakePeer()
  const errors = []
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/bounded-candidates",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: (error) => errors.push(error),
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  await eventually(() => peer.initialCandidateEmitted)
  for (let index = 0; index < 80; index += 1) {
    peer.emitCandidate(index + 100)
  }
  assert.equal(errors.length, 1)
  assert.match(errors[0].message, /too many local ICE candidates/)
  await assert.rejects(() => client.restart(), /session has failed/)
  await client.close()
})

test("WHEP terminal signaling remains contained when its observer throws", async () => {
  const peer = new FakePeer()
  let observations = 0
  const client = new WHEPClient({
    authorization: "Bearer viewer-token",
    endpoint: "https://edge.example/whep",
    fetch: async (_input, init) => {
      if (init.method === "POST") {
        return response(initialAnswer, 201, {
          "Content-Type": "application/sdp",
          ETag: '"generation-1"',
          Location: "/whep/throwing-observer",
        })
      }
      return response(null, init.method === "PATCH" ? 204 : 200)
    },
    iceServers: [],
    onError: () => {
      observations += 1
      throw new Error("observer failure")
    },
    onTrack: () => {},
    peerFactory: () => peer,
  })
  await client.start()
  await eventually(() => peer.initialCandidateEmitted)
  assert.doesNotThrow(() => {
    for (let index = 0; index < 80; index += 1) {
      peer.emitCandidate(index + 100)
    }
  })
  assert.equal(observations, 1)
  await assert.rejects(() => client.restart(), /session has failed/)
  await client.close()
})

class FakePeer {
  closed = false
  configuration = { iceServers: [] }
  connectionState = "new"
  createOfferCalls = 0
  initialCandidateEmitted = false
  localDescription = null
  onconnectionstatechange = null
  onicecandidate = null
  ontrack = null
  remoteDescription = null
  rollbackCalls = 0
  signalingState = "stable"

  addTransceiver() {}

  getConfiguration() {
    return this.configuration
  }

  setConfiguration(configuration) {
    this.configuration = configuration
  }

  async createOffer(options) {
    this.createOfferCalls += 1
    return {
      sdp: options?.iceRestart ? restartOffer : initialOffer,
      type: "offer",
    }
  }

  async createAnswer() {
    return { sdp: initialOffer, type: "answer" }
  }

  async setLocalDescription(description) {
    if (description.type === "rollback") {
      this.rollbackCalls += 1
      this.localDescription = null
      this.signalingState = "stable"
      return
    }
    this.localDescription = description
    this.signalingState =
      description.type === "offer" ? "have-local-offer" : "stable"
    const generation = this.createOfferCalls
    queueMicrotask(() => {
      if (this.closed || this.localDescription !== description) {
        return
      }
      this.onicecandidate?.({
        candidate: {
          toJSON: () => ({
            candidate: `candidate:client-${generation} 1 udp 2130706431 192.0.2.${generation + 10} ${4000 + generation} typ host`,
            sdpMid: "0",
            usernameFragment: `client-${generation}`,
          }),
        },
      })
      this.initialCandidateEmitted = true
      this.onicecandidate?.({ candidate: null })
    })
  }

  async setRemoteDescription(description) {
    this.remoteDescription = description
    if (description.type === "answer") {
      this.signalingState = "stable"
    } else if (description.type === "offer") {
      this.signalingState = "have-remote-offer"
    }
  }

  emitCandidate(index) {
    this.onicecandidate?.({
      candidate: {
        toJSON: () => ({
          candidate: `candidate:extra-${index} 1 udp 2130706431 192.0.2.20 ${5000 + index} typ host`,
          sdpMid: "0",
          usernameFragment: "client-1",
        }),
      },
    })
  }

  close() {
    this.closed = true
  }
}

class ManualCompletionPeer extends FakePeer {
  async setLocalDescription(description) {
    if (description.type === "rollback") {
      await super.setLocalDescription(description)
      return
    }
    this.localDescription = description
    this.signalingState =
      description.type === "offer" ? "have-local-offer" : "stable"
    queueMicrotask(() => {
      if (this.closed || this.localDescription !== description) {
        return
      }
      this.emitCandidate(1)
      this.initialCandidateEmitted = true
    })
  }

  completeGathering() {
    this.onicecandidate?.({ candidate: null })
  }
}

class EmbeddedCandidatePeer extends FakePeer {
  async setLocalDescription(description) {
    if (description.type === "rollback") {
      await super.setLocalDescription(description)
      return
    }
    this.localDescription = description
    this.signalingState =
      description.type === "offer" ? "have-local-offer" : "stable"
    const generation = this.createOfferCalls
    queueMicrotask(() => {
      if (this.closed || this.localDescription !== description) {
        return
      }
      const candidate = `candidate:client-${generation} 1 udp 2130706431 192.0.2.${generation + 10} ${4000 + generation} typ host`
      this.onicecandidate?.({
        candidate: {
          toJSON: () => ({
            candidate,
            sdpMid: "0",
            usernameFragment: `client-${generation}`,
          }),
        },
      })
      this.initialCandidateEmitted = true
      this.localDescription = {
        ...description,
        sdp: description.sdp.replace(
          "a=mid:0\r\n",
          `a=mid:0\r\na=${candidate}\r\n`,
        ),
      }
      this.onicecandidate?.({ candidate: null })
    })
  }
}

class ExpiringCounterOfferPeer extends FakePeer {
  constructor(expire) {
    super()
    this.expire = expire
  }

  async createAnswer() {
    this.expire()
    return super.createAnswer()
  }
}

function sdp({ candidate, direction, pwd, rtcpMuxOnly = false, ufrag }) {
  return [
    "v=0",
    "o=- 1 1 IN IP4 127.0.0.1",
    "s=-",
    "t=0 0",
    "a=group:BUNDLE 0",
    "a=ice-options:trickle",
    "m=video 9 UDP/TLS/RTP/SAVPF 96 97",
    "c=IN IP4 0.0.0.0",
    `a=ice-ufrag:${ufrag}`,
    `a=ice-pwd:${pwd}`,
    "a=fingerprint:sha-256 00:11:22:33",
    "a=setup:actpass",
    "a=mid:0",
    `a=${direction}`,
    "a=rtcp-mux",
    ...(rtcpMuxOnly ? ["a=rtcp-mux-only"] : []),
    ...(candidate ? [`a=${candidate}`] : []),
    "a=rtpmap:96 H264/90000",
    "a=rtpmap:97 rtx/90000",
    "a=fmtp:97 apt=96",
    "",
  ].join("\r\n")
}

function response(body, status, headers = {}) {
  return new Response(body, { headers, status })
}

function header(headers, name) {
  return new Headers(headers).get(name)
}

async function eventually(predicate, timeoutMilliseconds = 1000) {
  const deadline = Date.now() + timeoutMilliseconds
  while (!predicate()) {
    if (Date.now() >= deadline) {
      throw new Error("condition was not satisfied")
    }
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}
