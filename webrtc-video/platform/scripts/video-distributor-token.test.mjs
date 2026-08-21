import assert from "node:assert/strict"
import {
  createHash,
  createPublicKey,
  generateKeyPairSync,
  verify,
} from "node:crypto"
import test from "node:test"

import {
  MediaMTXTokenService,
  credentialExpiresAt,
  deviceIDFromMediaPath,
  mediaPath,
} from "../src/lib/video-distributor-token.ts"
import { mediaMTXSourceRequestSchema } from "../src/lib/validations/video-distributor.ts"

const deviceID = "fd8c2b34-1da2-4c71-8f38-343af59c0a11"

test("MediaMTX tokens bind one action to one device path", () => {
  const { privateKey, publicKey } = generateKeyPairSync("rsa", {
    modulusLength: 2048,
  })
  const service = new MediaMTXTokenService({
    audience: "rstream-mediamtx",
    issuer: "rstream-platform",
    privateKeyBase64: privateKey
      .export({ format: "der", type: "pkcs8" })
      .toString("base64"),
  })
  const now = new Date("2026-08-17T12:00:00.000Z")
  const token = service.sign({
    action: "read",
    now,
    path: mediaPath(deviceID),
    subject: `viewer:${deviceID}`,
    ttlSeconds: 300,
  })
  const [encodedHeader, encodedPayload, encodedSignature] = token.split(".")
  const signingInput = `${encodedHeader}.${encodedPayload}`
  assert.equal(
    verify(
      "RSA-SHA256",
      Buffer.from(signingInput),
      publicKey,
      Buffer.from(encodedSignature, "base64url"),
    ),
    true,
  )
  const header = decodeJSON(encodedHeader)
  const payload = decodeJSON(encodedPayload)
  assert.deepEqual(
    { alg: header.alg, kid: header.kid, typ: header.typ },
    { alg: "RS256", kid: service.jwks().keys[0].kid, typ: "JWT" },
  )
  assert.deepEqual(payload.mediamtx_permissions, [
    { action: "read", path: `devices/${deviceID}` },
  ])
  assert.equal(payload.iss, "rstream-platform")
  assert.equal(payload.aud, "rstream-mediamtx")
  assert.equal(payload.exp - payload.iat, 300)
  assert.equal(payload.nbf, payload.iat - 5)
  const jwk = service.jwks().keys[0]
  assert.equal(jwk.kty, "RSA")
  assert.equal("d" in jwk, false)
  assert.doesNotThrow(() => createPublicKey({ format: "jwk", key: jwk }))
})

test("media paths reject traversal and non-device identifiers", () => {
  assert.equal(deviceIDFromMediaPath(mediaPath(deviceID)), deviceID)
  for (const value of [
    "../devices/" + deviceID,
    "devices/" + deviceID + "/whep",
    "devices/not-a-uuid",
    "devices/" + deviceID + "?token=secret",
  ]) {
    assert.throws(() => deviceIDFromMediaPath(value), /Media path is invalid/)
  }
  assert.throws(() => mediaPath("not-a-uuid"), /not a UUID/)
})

test("credential deadlines preserve the original issue time", () => {
  const issuedAt = new Date("2026-08-17T12:00:00.250Z")
  assert.equal(
    credentialExpiresAt(issuedAt, 120).toISOString(),
    "2026-08-17T12:02:00.250Z",
  )
  assert.throws(
    () => credentialExpiresAt(new Date(Number.NaN), 120),
    /issue time is invalid/,
  )
  assert.throws(
    () => credentialExpiresAt(issuedAt, 0),
    /TTL must be a positive integer/,
  )
})

test("source resolution separates session setup from signaling refresh", () => {
  for (const purpose of ["session", "signaling"]) {
    assert.deepEqual(
      mediaMTXSourceRequestSchema.parse({
        path: `devices/${deviceID}`,
        purpose,
      }),
      { path: `devices/${deviceID}`, purpose },
    )
  }
  for (const value of [
    { path: `devices/${deviceID}` },
    { path: `devices/${deviceID}`, purpose: "turn" },
    { path: `devices/${deviceID}`, purpose: "signaling", extra: true },
  ]) {
    assert.throws(() => mediaMTXSourceRequestSchema.parse(value))
  }
})

test("MediaMTX token service rejects weak and malformed keys", () => {
  const weak = generateKeyPairSync("rsa", { modulusLength: 1024 }).privateKey
  assert.throws(
    () =>
      new MediaMTXTokenService({
        audience: "rstream-mediamtx",
        issuer: "rstream-platform",
        privateKeyBase64: weak
          .export({ format: "der", type: "pkcs8" })
          .toString("base64"),
      }),
    /at least 2048 bits/,
  )
  assert.throws(
    () =>
      new MediaMTXTokenService({
        audience: "rstream-mediamtx",
        issuer: "rstream-platform",
        privateKeyBase64: "not base64!",
      }),
    /not valid base64/,
  )
})

test("MediaMTX token service validates runtime claims and deadlines", () => {
  const active = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const service = new MediaMTXTokenService({
    audience: "rstream-mediamtx",
    issuer: "rstream-platform",
    privateKeyBase64: active.privateKey
      .export({ format: "der", type: "pkcs8" })
      .toString("base64"),
  })
  const valid = {
    action: "read",
    path: mediaPath(deviceID),
    subject: "viewer",
    ttlSeconds: 300,
  }
  assert.throws(
    () => service.sign({ ...valid, action: "admin" }),
    /action is invalid/,
  )
  assert.throws(
    () => service.sign({ ...valid, now: new Date(Number.NaN) }),
    /issue time is invalid/,
  )
  assert.throws(
    () => service.sign({ ...valid, ttlSeconds: 3601 }),
    /from 1 through 3600/,
  )
  assert.throws(
    () => service.sign({ ...valid, subject: "viewer\nadmin" }),
    /subject is invalid/,
  )
})

test("MediaMTX token service prepublishes the next public key before rotation", () => {
  const active = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const next = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const nextJWK = mediaMTXPublicJWK(next.publicKey)
  const service = new MediaMTXTokenService({
    additionalJWKS: JSON.stringify({ keys: [nextJWK] }),
    audience: "rstream-mediamtx",
    issuer: "rstream-platform",
    privateKeyBase64: active.privateKey
      .export({ format: "der", type: "pkcs8" })
      .toString("base64"),
  })
  const keys = service.jwks().keys
  assert.equal(keys.length, 2)
  assert.equal(keys[1].kid, nextJWK.kid)
  assert.equal("d" in keys[1], false)
  const token = service.sign({
    action: "read",
    path: mediaPath(deviceID),
    subject: "viewer",
    ttlSeconds: 60,
  })
  assert.equal(decodeJSON(token.split(".")[0]).kid, keys[0].kid)
  assert.notEqual(keys[0].kid, keys[1].kid)
})

test("MediaMTX token service retains the old public key after rotation", () => {
  const previous = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const active = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const previousJWK = mediaMTXPublicJWK(previous.publicKey)
  const activeJWK = mediaMTXPublicJWK(active.publicKey)
  const service = new MediaMTXTokenService({
    additionalJWKS: JSON.stringify({ keys: [previousJWK] }),
    audience: "rstream-mediamtx",
    issuer: "rstream-platform",
    privateKeyBase64: active.privateKey
      .export({ format: "der", type: "pkcs8" })
      .toString("base64"),
  })
  const keys = service.jwks().keys
  assert.deepEqual(
    keys.map((key) => key.kid),
    [activeJWK.kid, previousJWK.kid],
  )
  const token = service.sign({
    action: "read",
    path: mediaPath(deviceID),
    subject: "viewer",
    ttlSeconds: 60,
  })
  assert.equal(decodeJSON(token.split(".")[0]).kid, activeJWK.kid)
})

test("MediaMTX token service rejects unsafe additional key rings", () => {
  const active = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const previous = generateKeyPairSync("rsa", { modulusLength: 2048 })
  const previousJWK = mediaMTXPublicJWK(previous.publicKey)
  const options = {
    audience: "rstream-mediamtx",
    issuer: "rstream-platform",
    privateKeyBase64: active.privateKey
      .export({ format: "der", type: "pkcs8" })
      .toString("base64"),
  }
  assert.throws(
    () =>
      new MediaMTXTokenService({
        ...options,
        additionalJWKS: JSON.stringify({
          keys: [
            {
              ...previous.privateKey.export({ format: "jwk" }),
              alg: "RS256",
              kid: previousJWK.kid,
              use: "sig",
            },
          ],
        }),
      }),
    /invalid RSA key/,
  )
  assert.throws(
    () =>
      new MediaMTXTokenService({
        ...options,
        additionalJWKS: JSON.stringify({ keys: [previousJWK, previousJWK] }),
      }),
    /duplicate key identifier/,
  )
  const activeJWK = mediaMTXPublicJWK(active.publicKey)
  assert.throws(
    () =>
      new MediaMTXTokenService({
        ...options,
        additionalJWKS: JSON.stringify({ keys: [activeJWK] }),
      }),
    /duplicate key identifier/,
  )
  assert.throws(
    () =>
      new MediaMTXTokenService({
        ...options,
        additionalJWKS: JSON.stringify({
          keys: [{ ...previousJWK, kid: "A".repeat(43) }],
        }),
      }),
    /identifier does not match/,
  )
})

function mediaMTXPublicJWK(publicKey) {
  const publicDER = publicKey.export({ format: "der", type: "spki" })
  return {
    ...publicKey.export({ format: "jwk" }),
    alg: "RS256",
    kid: createHash("sha256").update(publicDER).digest("base64url"),
    use: "sig",
  }
}

function decodeJSON(encoded) {
  return JSON.parse(Buffer.from(encoded, "base64url").toString("utf8"))
}
