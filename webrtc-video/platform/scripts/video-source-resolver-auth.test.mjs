import assert from "node:assert/strict"
import { createHash, generateKeyPairSync, sign as signBytes } from "node:crypto"
import test from "node:test"

import { SourceResolverRequestVerifier } from "../src/lib/video-source-resolver-auth.ts"

const now = new Date("2026-08-18T12:00:00.000Z")
const path = "devices/9e1b1e39-6f98-47df-bf55-c2f05fab6739"
const purpose = "session"
const issuer = "rstream-video-distributor"
const audience = "rstream-video-source-resolver"

test("resolver identity binds one signed request to its instance and source", () => {
  const identity = signingIdentity("mediamtx-eu-1")
  const verifier = requestVerifier(identity.publicJWK)
  const authorization = token(identity, {})
  assert.equal(verifier.authorize(authorization, path, purpose, now), true)
  assert.equal(
    verifier.authorize(authorization, `${path}-other`, purpose, now),
    false,
  )
  assert.equal(verifier.authorize(authorization, path, "signaling", now), false)
  assert.equal(verifier.authorize(null, path, purpose, now), false)
})

test("resolver verifier accepts the Go signer golden vector", () => {
  const verifier = requestVerifier({
    alg: "EdDSA",
    crv: "Ed25519",
    kid: "moJRf5rxlBbZj9vPGTcms6lcC2_sHVGIS_Phtzm6LvQ",
    kty: "OKP",
    rstream_instance: "mediamtx-eu-1",
    use: "sig",
    x: "IVL40Zt5HSRFMkLhXy6rbLfP-ntqXtMAl5YOBpiB2xI",
  })
  const authorization =
    "Bearer eyJhbGciOiJFZERTQSIsImtpZCI6Im1vSlJmNXJ4bEJiWmo5dlBHVGNtczZsY0MyX3NIVkdJU19QaHR6bTZMdlEiLCJ0eXAiOiJKV1QifQ.eyJhdWQiOiJyc3RyZWFtLXZpZGVvLXNvdXJjZS1yZXNvbHZlciIsImV4cCI6MTc4NzA1NjUwOCwiaWF0IjoxNzg3MDU2NDk2LCJpc3MiOiJyc3RyZWFtLXZpZGVvLWRpc3RyaWJ1dG9yIiwianRpIjoicGFXbHBhV2xwYVdscGFXbHBhV2xwUSIsIm5iZiI6MTc4NzA1NjQ5MSwicGF0aCI6ImRldmljZXMvOWUxYjFlMzktNmY5OC00N2RmLWJmNTUtYzJmMDVmYWI2NzM5IiwicHVycG9zZSI6InNpZ25hbGluZyIsInN1YiI6Im1lZGlhbXR4LWV1LTEifQ.qEeHD899vL56mEJ-H2vYuCbb37-3KssRvuf0mNVYcTss9BShbnMK9AH9CEzNUyOKb-ioA2TUCw9xd8MWJobhAA" // gitleaks:allow -- deterministic public test vector
  assert.equal(
    verifier.authorize(
      authorization,
      path,
      "signaling",
      new Date("2026-08-18T12:34:56.000Z"),
    ),
    true,
  )
})

test("resolver identity supports bounded key rotation without sharing private keys", () => {
  const previous = signingIdentity("mediamtx-eu-1")
  const current = signingIdentity("mediamtx-eu-1")
  const anotherInstance = signingIdentity("mediamtx-us-1")
  const verifier = requestVerifier(
    previous.publicJWK,
    current.publicJWK,
    anotherInstance.publicJWK,
  )
  assert.equal(
    verifier.authorize(token(previous, {}), path, purpose, now),
    true,
  )
  assert.equal(verifier.authorize(token(current, {}), path, purpose, now), true)
  assert.equal(
    verifier.authorize(token(anotherInstance, {}), path, purpose, now),
    true,
  )
})

test("resolver identity fails closed on tampering, replay lifetime, and claim drift", () => {
  const identity = signingIdentity("mediamtx-eu-1")
  const verifier = requestVerifier(identity.publicJWK)
  const cases = [
    token(identity, { audience: "another-audience" }),
    token(identity, { issuer: "another-issuer" }),
    token(identity, { subject: "mediamtx-us-1" }),
    token(identity, { issuedAt: epoch(now) - 60, expiresAt: epoch(now) - 10 }),
    token(identity, { issuedAt: epoch(now) + 10, expiresAt: epoch(now) + 20 }),
    token(identity, { expiresAt: epoch(now) + 31 }),
    token(identity, { notBefore: epoch(now) + 10 }),
    token(identity, { nonce: "short" }),
    token(identity, { extraClaim: true }),
  ]
  for (const authorization of cases) {
    assert.equal(verifier.authorize(authorization, path, purpose, now), false)
  }
  const valid = token(identity, {})
  const [header, payload, signature] = valid.slice("Bearer ".length).split(".")
  const tamperedPayload = encodeJSON({
    ...decodeJSON(payload),
    path: `${path}-tampered`,
  })
  assert.equal(
    verifier.authorize(
      `Bearer ${header}.${tamperedPayload}.${signature}`,
      `${path}-tampered`,
      purpose,
      now,
    ),
    false,
  )
  assert.equal(verifier.authorize(`${valid}.extra`, path, purpose, now), false)
  assert.equal(verifier.authorize("Basic value", path, purpose, now), false)
})

test("resolver verifier rejects malformed and ambiguous key sets", () => {
  const identity = signingIdentity("mediamtx-eu-1")
  assert.throws(() => requestVerifier(), /must contain 1-64 keys/)
  assert.throws(
    () => requestVerifier(identity.publicJWK, identity.publicJWK),
    /duplicate key identifier/,
  )
  assert.throws(
    () =>
      requestVerifier({
        ...identity.publicJWK,
        rstream_instance: "unsafe instance",
      }),
    /invalid Ed25519 key/,
  )
  assert.throws(
    () =>
      requestVerifier({
        ...identity.publicJWK,
        kid: "A".repeat(43),
      }),
    /identifier does not match/,
  )
  const { publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 })
  assert.throws(
    () =>
      requestVerifier({
        ...publicKey.export({ format: "jwk" }),
        alg: "RS256",
        kid: "rsa",
        rstream_instance: "mediamtx-eu-1",
        use: "sig",
      }),
    /invalid Ed25519 key/,
  )
  assert.throws(
    () =>
      new SourceResolverRequestVerifier({
        audience: "unsafe audience",
        issuer,
        jwks: JSON.stringify({ keys: [identity.publicJWK] }),
      }),
    /audience is invalid/,
  )
})

function requestVerifier(...keys) {
  return new SourceResolverRequestVerifier({
    audience,
    issuer,
    jwks: JSON.stringify({ keys }),
  })
}

function signingIdentity(instance) {
  const { privateKey, publicKey } = generateKeyPairSync("ed25519")
  const publicDER = publicKey.export({ format: "der", type: "spki" })
  const keyID = createHash("sha256").update(publicDER).digest("base64url")
  return {
    instance,
    keyID,
    privateKey,
    publicJWK: {
      ...publicKey.export({ format: "jwk" }),
      alg: "EdDSA",
      kid: keyID,
      rstream_instance: instance,
      use: "sig",
    },
  }
}

function token(identity, overrides) {
  const issuedAt = overrides.issuedAt ?? epoch(now)
  const header = encodeJSON({ alg: "EdDSA", kid: identity.keyID, typ: "JWT" })
  const payload = encodeJSON({
    aud: overrides.audience ?? audience,
    exp: overrides.expiresAt ?? issuedAt + 20,
    iat: issuedAt,
    iss: overrides.issuer ?? issuer,
    jti: overrides.nonce ?? Buffer.alloc(16, 0xa5).toString("base64url"),
    nbf: overrides.notBefore ?? issuedAt - 5,
    path: overrides.path ?? path,
    purpose: overrides.purpose ?? purpose,
    sub: overrides.subject ?? identity.instance,
    ...(overrides.extraClaim ? { unexpected: true } : {}),
  })
  const input = `${header}.${payload}`
  const signature = signBytes(null, Buffer.from(input), identity.privateKey)
  return `Bearer ${input}.${signature.toString("base64url")}`
}

function encodeJSON(value) {
  return Buffer.from(JSON.stringify(value)).toString("base64url")
}

function decodeJSON(value) {
  return JSON.parse(Buffer.from(value, "base64url").toString("utf8"))
}

function epoch(value) {
  return Math.floor(value.getTime() / 1000)
}
