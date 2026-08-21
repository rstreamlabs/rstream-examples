import assert from "node:assert/strict"
import { createHash, createPrivateKey, createPublicKey } from "node:crypto"
import { execFile } from "node:child_process"
import { promisify } from "node:util"
import test from "node:test"

const execute = promisify(execFile)

test("MediaMTX key generator produces separate valid platform and instance identities", async () => {
  const { stdout } = await execute(
    process.execPath,
    ["scripts/generate-mediamtx-key.mjs", "mediamtx-eu-1"],
    { maxBuffer: 64 * 1024 },
  )
  const values = generatedValues(stdout)
  const platformKey = createPrivateKey({
    format: "der",
    key: Buffer.from(values.MEDIAMTX_JWT_PRIVATE_KEY_BASE64, "base64"),
    type: "pkcs8",
  })
  assert.equal(platformKey.asymmetricKeyType, "rsa")
  assert.ok((platformKey.asymmetricKeyDetails?.modulusLength ?? 0) >= 3072)
  const platformPublic = createPublicKey(platformKey)
  const platformPublicDER = platformPublic.export({
    format: "der",
    type: "spki",
  })
  assert.deepEqual(JSON.parse(values.MEDIAMTX_JWT_PUBLIC_JWK), {
    ...platformPublic.export({ format: "jwk" }),
    alg: "RS256",
    kid: createHash("sha256").update(platformPublicDER).digest("base64url"),
    use: "sig",
  })
  const resolverKey = createPrivateKey({
    format: "der",
    key: Buffer.from(
      values.RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64,
      "base64",
    ),
    type: "pkcs8",
  })
  assert.equal(resolverKey.asymmetricKeyType, "ed25519")
  const resolverPublic = createPublicKey(resolverKey)
  const publicDER = resolverPublic.export({ format: "der", type: "spki" })
  const expectedKeyID = createHash("sha256")
    .update(publicDER)
    .digest("base64url")
  const jwks = JSON.parse(values.MEDIAMTX_SOURCE_RESOLVER_JWKS)
  assert.deepEqual(jwks, {
    keys: [
      {
        ...resolverPublic.export({ format: "jwk" }),
        alg: "EdDSA",
        kid: expectedKeyID,
        rstream_instance: "mediamtx-eu-1",
        use: "sig",
      },
    ],
  })
  assert.equal(values.RSTREAM_SOURCE_RESOLVER_INSTANCE_ID, "mediamtx-eu-1")
})

test("MediaMTX key generator rejects an unsafe instance identity", async () => {
  await assert.rejects(
    execute(
      process.execPath,
      ["scripts/generate-mediamtx-key.mjs", "unsafe instance"],
      { maxBuffer: 64 * 1024 },
    ),
  )
})

function generatedValues(output) {
  const values = {}
  for (const line of output.trim().split("\n")) {
    const match = line.match(/^([A-Z0-9_]+)=(['"])(.*)\2$/)
    assert.ok(match, `unexpected generator output for ${match?.[1] ?? "line"}`)
    values[match[1]] = match[3]
  }
  return values
}
