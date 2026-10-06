import assert from "node:assert/strict"
import test from "node:test"
import {
  deviceAccessConfig,
  deviceOwner,
  deviceOwnerLabels,
  deviceOwnerWhere,
} from "../src/lib/device-access.ts"
import {
  GitHubMembershipVerifier,
  MembershipUnavailable,
} from "../src/lib/github-membership.ts"

const membership = (state = "active", id = 42) => ({
  state,
  organization: { id, login: "ACME" },
  user: { id: 7 },
})

test("ownership isolates users and shares stable organization identities", () => {
  assert.equal(deviceAccessConfig({}).mode, "user")
  assert.throws(() =>
    deviceAccessConfig({ DEVICE_ACCESS_MODE: "organization" }),
  )
  assert.throws(() => deviceAccessConfig({ DEVICE_ACCESS_MODE: "other" }))
  const first = { kind: "user", id: "first" }
  const second = { kind: "user", id: "second" }
  const org = { kind: "organization", id: "42" }
  assert.notDeepEqual(deviceOwnerWhere(first), deviceOwnerWhere(second))
  assert.deepEqual(deviceOwnerWhere(org), {
    organizationId: "42",
    userId: null,
  })
  assert.deepEqual(deviceOwnerLabels(org), { organization: "42" })
  assert.deepEqual(deviceOwnerLabels(first), { user: "first" })
  assert.deepEqual(deviceOwner({ userId: null, organizationId: "42" }), org)
  assert.throws(() => deviceOwner({ userId: "first", organizationId: "42" }))
  assert.throws(() => deviceOwner({ userId: null, organizationId: null }))
})

test("membership checks use authenticated identity, active membership and bounded caching", async () => {
  let now = 1000,
    calls = 0,
    state = "active"
  const verifier = new GitHubMembershipVerifier({
    now: () => now,
    fetch: async (url, options) => {
      calls++
      assert.equal(url, "https://api.github.com/user/memberships/orgs/acme")
      assert.equal(options.redirect, "error")
      assert.equal(options.cache, "no-store")
      assert.ok(options.signal)
      return Response.json(membership(state))
    },
  })
  assert.deepEqual(await verifier.verify("acme", "7", "token"), {
    organizationId: "42",
  })
  state = "pending"
  now += 59_999
  assert.ok(await verifier.verify("acme", "7", "token"))
  now++
  assert.equal(await verifier.verify("acme", "7", "token"), null)
  assert.equal(calls, 2)
  assert.equal(await verifier.verify("acme", "8", "token"), null)
  assert.equal(await verifier.verify("../acme", "7", "token"), null)
})

test("civil-clock rollback cannot extend the default membership cache lifetime", async (t) => {
  let wallTime = 1_000_000
  let elapsedTime = 1_000
  let calls = 0
  let state = "active"
  t.mock.method(Date, "now", () => wallTime)
  t.mock.method(performance, "now", () => elapsedTime)
  const verifier = new GitHubMembershipVerifier({
    fetch: async () => {
      calls++
      return Response.json(membership(state))
    },
  })
  assert.ok(await verifier.verify("acme", "7", "token"))
  elapsedTime += 30_000
  wallTime += 30_000
  assert.ok(await verifier.verify("acme", "7", "token"))
  assert.equal(calls, 1)

  state = "pending"
  elapsedTime += 30_000
  // Civil time remains later than cache creation, so checking only that lower
  // bound does not prevent a rollback from extending the 60-second lifetime.
  wallTime -= 20_000
  assert.equal(await verifier.verify("acme", "7", "token"), null)
  assert.equal(calls, 2)

  state = "active"
  elapsedTime += 5_000
  assert.ok(await verifier.verify("acme", "7", "token"))
  assert.equal(calls, 3)
})

test("revoked credentials, wrong organization and wrong user never grant access", async () => {
  for (const response of [
    new Response(null, { status: 401 }),
    new Response(null, { status: 403 }),
    new Response(null, { status: 404 }),
    Response.json({ ...membership(), user: { id: 8 } }),
    Response.json({
      ...membership(),
      organization: { id: 42, login: "other" },
    }),
    Response.json(membership("pending")),
  ]) {
    const verifier = new GitHubMembershipVerifier({
      fetch: async () => response,
    })
    assert.equal(await verifier.verify("acme", "7", "token"), null)
  }
})

test("failed refresh never extends stale membership or leaks upstream errors", async () => {
  let now = 1000,
    fail = false
  const verifier = new GitHubMembershipVerifier({
    now: () => now,
    fetch: async () => {
      if (fail) throw new Error("secret token upstream error")
      return Response.json(membership())
    },
  })
  await verifier.verify("acme", "7", "token")
  fail = true
  now += 60_000
  await assert.rejects(
    verifier.verify("acme", "7", "token"),
    (error) =>
      error instanceof MembershipUnavailable &&
      !error.message.includes("secret"),
  )
})

test("concurrent membership checks coalesce and cancellation remains caller-local", async () => {
  let complete,
    calls = 0
  const verifier = new GitHubMembershipVerifier({
    fetch: () => {
      calls++
      return new Promise((resolve) => {
        complete = resolve
      })
    },
  })
  const controller = new AbortController()
  const first = verifier.verify("acme", "7", "token", controller.signal)
  const second = verifier.verify("acme", "7", "token")
  controller.abort(new Error("cancelled"))
  await assert.rejects(first, /cancelled/)
  complete(Response.json(membership()))
  assert.deepEqual(await second, { organizationId: "42" })
  assert.equal(calls, 1)
})

test("malformed and oversized membership responses fail closed", async () => {
  for (const response of [
    new Response("{"),
    new Response(" ".repeat(65537)),
    new Response(null, { status: 429 }),
    Response.json({
      ...membership(),
      organization: { id: 1e20, login: "acme" },
    }),
  ]) {
    const verifier = new GitHubMembershipVerifier({
      fetch: async () => response,
    })
    await assert.rejects(
      verifier.verify("acme", "7", "token"),
      MembershipUnavailable,
    )
  }
})
