import {
  createHash,
  createPrivateKey,
  createPublicKey,
  randomUUID,
  sign as signBytes,
  type KeyObject,
} from "node:crypto"

export type MediaMTXAction = "publish" | "read"

export type MediaMTXTokenOptions = {
  action: MediaMTXAction
  now?: Date
  path: string
  subject: string
  ttlSeconds: number
}

type MediaMTXTokenServiceOptions = {
  additionalJWKS?: string
  audience: string
  issuer: string
  privateKeyBase64: string
}

const deviceIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const mediaPathPattern =
  /^devices\/([0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})$/i
const maximumTokenTTLSeconds = 3600

export class MediaMTXTokenService {
  private readonly audience: string
  private readonly issuer: string
  private readonly keyID: string
  private readonly privateKey: KeyObject
  private readonly publicJWK: JsonWebKey & {
    alg: string
    kid: string
    use: string
  }
  private readonly additionalPublicJWKs: (JsonWebKey & {
    alg: string
    kid: string
    use: string
  })[]

  constructor(options: MediaMTXTokenServiceOptions) {
    this.audience = requiredClaim(options.audience, "audience")
    this.issuer = requiredClaim(options.issuer, "issuer")
    this.privateKey = parsePrivateKey(options.privateKeyBase64)
    const publicKey = createPublicKey(
      this.privateKey as unknown as Parameters<typeof createPublicKey>[0],
    )
    const publicDER = publicKey.export({ format: "der", type: "spki" })
    this.keyID = createHash("sha256").update(publicDER).digest("base64url")
    this.publicJWK = {
      ...publicKey.export({ format: "jwk" }),
      alg: "RS256",
      kid: this.keyID,
      use: "sig",
    }
    this.additionalPublicJWKs = parseAdditionalPublicKeys(
      options.additionalJWKS ?? "",
      this.keyID,
    )
  }

  jwks() {
    return {
      keys: [
        { ...this.publicJWK },
        ...this.additionalPublicJWKs.map((key) => ({ ...key })),
      ],
    }
  }

  sign(options: MediaMTXTokenOptions) {
    const path = validateMediaPath(options.path)
    const subject = requiredClaim(options.subject, "subject")
    if (options.action !== "publish" && options.action !== "read") {
      throw new Error("MediaMTX token action is invalid")
    }
    if (
      !Number.isSafeInteger(options.ttlSeconds) ||
      options.ttlSeconds < 1 ||
      options.ttlSeconds > maximumTokenTTLSeconds
    ) {
      throw new Error(
        `MediaMTX token TTL must be an integer from 1 through ${maximumTokenTTLSeconds}`,
      )
    }
    const now = Math.floor((options.now ?? new Date()).getTime() / 1000)
    if (!Number.isSafeInteger(now)) {
      throw new Error("MediaMTX token issue time is invalid")
    }
    const header = encodeJSON({ alg: "RS256", kid: this.keyID, typ: "JWT" })
    const payload = encodeJSON({
      aud: this.audience,
      exp: now + options.ttlSeconds,
      iat: now,
      iss: this.issuer,
      jti: randomUUID(),
      mediamtx_permissions: [{ action: options.action, path }],
      nbf: now - 5,
      sub: subject,
    })
    const signingInput = `${header}.${payload}`
    const signature = signBytes(
      "RSA-SHA256",
      Buffer.from(signingInput),
      this.privateKey,
    ).toString("base64url")
    return `${signingInput}.${signature}`
  }
}

export function mediaPath(deviceID: string) {
  const normalized = deviceID.trim().toLowerCase()
  if (!deviceIDPattern.test(normalized)) {
    throw new Error("Device identifier is not a UUID")
  }
  return `devices/${normalized}`
}

export function deviceIDFromMediaPath(path: string) {
  const match = path.trim().match(mediaPathPattern)
  if (!match) {
    throw new Error("Media path is invalid")
  }
  return match[1].toLowerCase()
}

export function credentialExpiresAt(issuedAt: Date, ttlSeconds: number) {
  const issuedAtMilliseconds = issuedAt.getTime()
  if (!Number.isFinite(issuedAtMilliseconds)) {
    throw new Error("Credential issue time is invalid")
  }
  if (!Number.isSafeInteger(ttlSeconds) || ttlSeconds < 1) {
    throw new Error("Credential TTL must be a positive integer")
  }
  return new Date(issuedAtMilliseconds + ttlSeconds * 1000)
}

function validateMediaPath(path: string) {
  const match = path.trim().match(mediaPathPattern)
  if (!match) {
    throw new Error("MediaMTX permission path is invalid")
  }
  return `devices/${match[1].toLowerCase()}`
}

function parsePrivateKey(encoded: string) {
  const value = encoded.trim()
  if (!value || !/^[A-Za-z0-9+/]+={0,2}$/.test(value)) {
    throw new Error("MediaMTX signing key is not valid base64")
  }
  let key: KeyObject
  try {
    key = createPrivateKey({
      format: "der",
      key: Buffer.from(value, "base64"),
      type: "pkcs8",
    })
  } catch {
    throw new Error("MediaMTX signing key is not a PKCS#8 private key")
  }
  if (
    key.asymmetricKeyType !== "rsa" ||
    (key.asymmetricKeyDetails?.modulusLength ?? 0) < 2048
  ) {
    throw new Error(
      "MediaMTX signing key must be an RSA key of at least 2048 bits",
    )
  }
  return key
}

function parseAdditionalPublicKeys(
  raw: string,
  activeKeyID: string,
): (JsonWebKey & { alg: string; kid: string; use: string })[] {
  const value = raw.trim()
  if (!value) {
    return []
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    throw new Error("MediaMTX additional JWKS is not valid JSON")
  }
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    !Array.isArray((parsed as { keys?: unknown }).keys)
  ) {
    throw new Error("MediaMTX additional JWKS must contain a keys array")
  }
  const candidates = (parsed as { keys: unknown[] }).keys
  if (candidates.length > 8) {
    throw new Error(
      "MediaMTX additional JWKS must not contain more than 8 keys",
    )
  }
  const seen = new Set([activeKeyID])
  return candidates.map((candidate) => {
    if (
      typeof candidate !== "object" ||
      candidate === null ||
      (candidate as JsonWebKey).kty !== "RSA" ||
      (candidate as { alg?: unknown }).alg !== "RS256" ||
      (candidate as { use?: unknown }).use !== "sig" ||
      typeof (candidate as { kid?: unknown }).kid !== "string" ||
      !/^[A-Za-z0-9_-]{43}$/.test(String((candidate as { kid: string }).kid)) ||
      hasPrivateRSAParameter(candidate as Record<string, unknown>)
    ) {
      throw new Error("MediaMTX additional JWKS contains an invalid RSA key")
    }
    const keyID = String((candidate as { kid: string }).kid)
    if (seen.has(keyID)) {
      throw new Error(
        "MediaMTX additional JWKS contains a duplicate key identifier",
      )
    }
    let key: KeyObject
    try {
      key = createPublicKey({ format: "jwk", key: candidate as JsonWebKey })
    } catch {
      throw new Error("MediaMTX additional JWKS contains an invalid RSA key")
    }
    if (
      key.asymmetricKeyType !== "rsa" ||
      (key.asymmetricKeyDetails?.modulusLength ?? 0) < 2048
    ) {
      throw new Error(
        "MediaMTX additional JWKS keys must be RSA keys of at least 2048 bits",
      )
    }
    const computedKeyID = createHash("sha256")
      .update(key.export({ format: "der", type: "spki" }))
      .digest("base64url")
    if (keyID !== computedKeyID) {
      throw new Error(
        "MediaMTX additional JWKS key identifier does not match its public key",
      )
    }
    seen.add(keyID)
    return {
      ...key.export({ format: "jwk" }),
      alg: "RS256",
      kid: keyID,
      use: "sig",
    }
  })
}

function hasPrivateRSAParameter(value: Record<string, unknown>) {
  return ["d", "p", "q", "dp", "dq", "qi", "oth"].some((name) => name in value)
}

function requiredClaim(value: string, name: string) {
  const normalized = value.trim()
  if (
    !normalized ||
    normalized.length > 512 ||
    /[\u0000-\u0020]/.test(normalized)
  ) {
    throw new Error(`MediaMTX token ${name} is invalid`)
  }
  return normalized
}

function encodeJSON(value: unknown) {
  return Buffer.from(JSON.stringify(value)).toString("base64url")
}
