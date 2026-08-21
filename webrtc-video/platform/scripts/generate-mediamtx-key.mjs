import { createHash, createPublicKey, generateKeyPairSync } from "node:crypto"

const instance = process.argv[2] ?? "mediamtx-one"
if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(instance)) {
  throw new Error(
    "MediaMTX instance must contain only letters, numbers, dots, dashes, and underscores.",
  )
}

const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 3072 })
const encodedKey = privateKey
  .export({ format: "der", type: "pkcs8" })
  .toString("base64")
const mediaMTXPublicKey = createPublicKey(privateKey)
const mediaMTXPublicDER = mediaMTXPublicKey.export({
  format: "der",
  type: "spki",
})
const mediaMTXKeyID = createHash("sha256")
  .update(mediaMTXPublicDER)
  .digest("base64url")
const mediaMTXPublicJWK = JSON.stringify({
  ...mediaMTXPublicKey.export({ format: "jwk" }),
  alg: "RS256",
  kid: mediaMTXKeyID,
  use: "sig",
})
const resolver = generateKeyPairSync("ed25519")
const resolverPrivateKey = resolver.privateKey
  .export({ format: "der", type: "pkcs8" })
  .toString("base64")
const resolverPublicDER = resolver.publicKey.export({
  format: "der",
  type: "spki",
})
const resolverKeyID = createHash("sha256")
  .update(resolverPublicDER)
  .digest("base64url")
const resolverJWKS = JSON.stringify({
  keys: [
    {
      ...resolver.publicKey.export({ format: "jwk" }),
      alg: "EdDSA",
      kid: resolverKeyID,
      rstream_instance: instance,
      use: "sig",
    },
  ],
})

process.stdout.write(
  [
    `MEDIAMTX_JWT_PRIVATE_KEY_BASE64="${encodedKey}"`,
    `MEDIAMTX_JWT_PUBLIC_JWK='${mediaMTXPublicJWK}'`,
    `MEDIAMTX_SOURCE_RESOLVER_JWKS='${resolverJWKS}'`,
    `RSTREAM_SOURCE_RESOLVER_INSTANCE_ID="${instance}"`,
    `RSTREAM_SOURCE_RESOLVER_PRIVATE_KEY_BASE64="${resolverPrivateKey}"`,
    "",
  ].join("\n"),
)
