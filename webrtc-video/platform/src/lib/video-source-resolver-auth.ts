import {
  createHash,
  createPublicKey,
  verify as verifyBytes,
  type KeyObject,
} from "node:crypto"

export type SourceResolutionPurpose = "session" | "signaling"

type ResolverVerifierOptions = {
  audience: string
  issuer: string
  jwks: string
}

type ResolverKey = {
  instance: string
  publicKey: KeyObject
}

const maximumAuthorizationBytes = 8 * 1024
const maximumHeaderBytes = 1024
const maximumPayloadBytes = 4 * 1024
const maximumKeys = 64
const maximumTokenLifetimeSeconds = 30
const clockSkewSeconds = 5
const instancePattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const keyIDPattern = /^[A-Za-z0-9_-]{43}$/
const compactTokenPattern = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/

export class SourceResolverRequestVerifier {
  private readonly audience: string
  private readonly issuer: string
  private readonly keys: Map<string, ResolverKey>

  constructor(options: ResolverVerifierOptions) {
    this.audience = requiredClaim(options.audience, "audience")
    this.issuer = requiredClaim(options.issuer, "issuer")
    this.keys = parseResolverKeys(options.jwks)
  }

  authorize(
    authorization: string | null,
    path: string,
    purpose: SourceResolutionPurpose,
    now = new Date(),
  ) {
    try {
      return this.verify(authorization, path, purpose, now)
    } catch {
      return false
    }
  }

  private verify(
    authorization: string | null,
    path: string,
    purpose: SourceResolutionPurpose,
    now: Date,
  ) {
    const value = authorization?.trim() ?? ""
    if (!value || value.length > maximumAuthorizationBytes) {
      return false
    }
    const compact = value.match(/^Bearer\s+(\S+)$/i)?.[1] ?? ""
    if (!compactTokenPattern.test(compact)) {
      return false
    }
    const [encodedHeader, encodedPayload, encodedSignature] = compact.split(".")
    const header = decodeJSON(encodedHeader, maximumHeaderBytes)
    if (!hasExactKeys(header, ["alg", "kid", "typ"])) {
      return false
    }
    if (header.alg !== "EdDSA" || header.typ !== "JWT") {
      return false
    }
    const key =
      typeof header.kid === "string" ? this.keys.get(header.kid) : null
    if (!key) {
      return false
    }
    const signature = decodeBase64URL(encodedSignature, ed25519SignatureBytes)
    if (
      !verifyBytes(
        null,
        Buffer.from(`${encodedHeader}.${encodedPayload}`),
        key.publicKey,
        signature,
      )
    ) {
      return false
    }
    const claims = decodeJSON(encodedPayload, maximumPayloadBytes)
    if (
      !hasExactKeys(claims, [
        "aud",
        "exp",
        "iat",
        "iss",
        "jti",
        "nbf",
        "path",
        "purpose",
        "sub",
      ])
    ) {
      return false
    }
    const nowSeconds = Math.floor(now.getTime() / 1000)
    if (!Number.isSafeInteger(nowSeconds)) {
      return false
    }
    if (
      claims.aud !== this.audience ||
      claims.iss !== this.issuer ||
      claims.sub !== key.instance ||
      claims.path !== path ||
      claims.purpose !== purpose ||
      !validTimeClaims(claims, nowSeconds) ||
      typeof claims.jti !== "string" ||
      !validNonce(claims.jti)
    ) {
      return false
    }
    return true
  }
}

const ed25519SignatureBytes = 64

function parseResolverKeys(raw: string) {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    throw new Error("MediaMTX resolver JWKS is not valid JSON")
  }
  if (!isRecord(parsed) || !Array.isArray(parsed.keys)) {
    throw new Error("MediaMTX resolver JWKS must contain a keys array")
  }
  if (parsed.keys.length < 1 || parsed.keys.length > maximumKeys) {
    throw new Error(`MediaMTX resolver JWKS must contain 1-${maximumKeys} keys`)
  }
  const keys = new Map<string, ResolverKey>()
  for (const candidate of parsed.keys) {
    if (
      !isRecord(candidate) ||
      candidate.kty !== "OKP" ||
      candidate.crv !== "Ed25519" ||
      candidate.alg !== "EdDSA" ||
      candidate.use !== "sig" ||
      typeof candidate.kid !== "string" ||
      !keyIDPattern.test(candidate.kid) ||
      !instancePattern.test(String(candidate.rstream_instance ?? ""))
    ) {
      throw new Error("MediaMTX resolver JWKS contains an invalid Ed25519 key")
    }
    if (keys.has(candidate.kid)) {
      throw new Error(
        "MediaMTX resolver JWKS contains a duplicate key identifier",
      )
    }
    if (typeof candidate.x !== "string") {
      throw new Error("MediaMTX resolver JWKS contains an invalid public key")
    }
    decodeBase64URL(candidate.x, 32)
    let publicKey: KeyObject
    try {
      publicKey = createPublicKey({
        format: "jwk",
        key: { crv: "Ed25519", kty: "OKP", x: candidate.x },
      })
    } catch {
      throw new Error("MediaMTX resolver JWKS contains an invalid public key")
    }
    if (publicKey.asymmetricKeyType !== "ed25519") {
      throw new Error("MediaMTX resolver JWKS key must use Ed25519")
    }
    const keyID = createHash("sha256")
      .update(publicKey.export({ format: "der", type: "spki" }))
      .digest("base64url")
    if (candidate.kid !== keyID) {
      throw new Error(
        "MediaMTX resolver JWKS key identifier does not match its public key",
      )
    }
    keys.set(candidate.kid, {
      instance: String(candidate.rstream_instance),
      publicKey,
    })
  }
  return keys
}

function validTimeClaims(claims: Record<string, unknown>, now: number) {
  if (
    !Number.isSafeInteger(claims.iat) ||
    !Number.isSafeInteger(claims.nbf) ||
    !Number.isSafeInteger(claims.exp)
  ) {
    return false
  }
  const issuedAt = Number(claims.iat)
  const notBefore = Number(claims.nbf)
  const expiresAt = Number(claims.exp)
  return (
    issuedAt <= now + clockSkewSeconds &&
    issuedAt >= now - maximumTokenLifetimeSeconds - clockSkewSeconds &&
    notBefore <= issuedAt &&
    notBefore >= issuedAt - clockSkewSeconds &&
    notBefore <= now + clockSkewSeconds &&
    expiresAt > now - clockSkewSeconds &&
    expiresAt > issuedAt &&
    expiresAt - issuedAt <= maximumTokenLifetimeSeconds
  )
}

function validNonce(value: string) {
  try {
    decodeBase64URL(value, 16)
    return true
  } catch {
    return false
  }
}

function decodeJSON(raw: string, maximumBytes: number) {
  const decoded = decodeBase64URL(raw)
  if (decoded.byteLength > maximumBytes) {
    throw new Error("resolver request token component is too large")
  }
  const value = JSON.parse(
    new TextDecoder("utf-8", { fatal: true }).decode(decoded),
  )
  if (!isRecord(value)) {
    throw new Error("resolver request token component is invalid")
  }
  return value
}

function decodeBase64URL(raw: string, expectedBytes?: number) {
  if (!raw || !/^[A-Za-z0-9_-]+$/.test(raw)) {
    throw new Error("resolver request token uses invalid base64url")
  }
  const decoded = Buffer.from(raw, "base64url")
  if (decoded.toString("base64url") !== raw) {
    throw new Error("resolver request token uses non-canonical base64url")
  }
  if (expectedBytes !== undefined && decoded.byteLength !== expectedBytes) {
    throw new Error("resolver request token component has an invalid size")
  }
  return decoded
}

function requiredClaim(raw: string, name: string) {
  const value = raw.trim()
  if (!value || value.length > 512 || /[\u0000-\u0020]/.test(value)) {
    throw new Error(`MediaMTX resolver ${name} is invalid`)
  }
  return value
}

function hasExactKeys(value: Record<string, unknown>, expected: string[]) {
  const actual = Object.keys(value).sort()
  const sortedExpected = [...expected].sort()
  return (
    actual.length === sortedExpected.length &&
    actual.every((key, index) => key === sortedExpected[index])
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
