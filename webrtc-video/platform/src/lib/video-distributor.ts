import "server-only"

import { createHash } from "node:crypto"

import { MediaMTXTokenService } from "./video-distributor-token"
import { SourceResolverRequestVerifier } from "./video-source-resolver-auth"
import { type SourceResolutionPurpose } from "./video-source-resolver-auth"
import { credentialExpiresAt } from "./video-distributor-token"
import { deviceIDFromMediaPath } from "./video-distributor-token"
import { mediaPath } from "./video-distributor-token"
import { rstreamEnv } from "@/lib/env"

let tokenServiceCache:
  { identity: string; service: MediaMTXTokenService } | undefined
let resolverVerifierCache:
  { identity: string; verifier: SourceResolverRequestVerifier } | undefined

export function videoDistributorMode() {
  return rstreamEnv().VIDEO_DISTRIBUTOR
}

export function mediaMTXPath(deviceID: string) {
  return mediaPath(deviceID)
}

export function mediaMTXDeviceID(path: string) {
  return deviceIDFromMediaPath(path)
}

export type ExpiringCredential = {
  expiresAt: Date
  token: string
}

export function mediaMTXViewerCredential(deviceID: string) {
  const env = rstreamEnv()
  const path = mediaPath(deviceID)
  return mediaMTXCredential({
    action: "read",
    path,
    subject: `viewer:${deviceID}`,
    ttlSeconds: env.MEDIAMTX_TOKEN_TTL_SECONDS,
  })
}

export function mediaMTXPublisherCredential(deviceID: string) {
  const env = rstreamEnv()
  const path = mediaPath(deviceID)
  return mediaMTXCredential({
    action: "publish",
    path,
    subject: `distributor:${deviceID}`,
    ttlSeconds: env.MEDIAMTX_TOKEN_TTL_SECONDS,
  })
}

export function mediaMTXJWKS() {
  return tokenService().jwks()
}

export function mediaMTXResolverAuthorized(
  request: Request,
  path: string,
  purpose: SourceResolutionPurpose,
) {
  return resolverVerifier().authorize(
    request.headers.get("authorization"),
    path,
    purpose,
  )
}

function mediaMTXCredential(options: {
  action: "publish" | "read"
  path: string
  subject: string
  ttlSeconds: number
}): ExpiringCredential {
  const issuedAt = new Date()
  return {
    expiresAt: credentialExpiresAt(issuedAt, options.ttlSeconds),
    token: tokenService().sign({ ...options, now: issuedAt }),
  }
}

function tokenService() {
  const env = rstreamEnv()
  const encodedKey = env.MEDIAMTX_JWT_PRIVATE_KEY_BASE64 ?? ""
  const additionalJWKS = env.MEDIAMTX_JWT_ADDITIONAL_JWKS ?? ""
  const identity = configurationIdentity([
    env.MEDIAMTX_JWT_ISSUER,
    env.MEDIAMTX_JWT_AUDIENCE,
    encodedKey,
    additionalJWKS,
  ])
  if (tokenServiceCache?.identity === identity) {
    return tokenServiceCache.service
  }
  const service = new MediaMTXTokenService({
    audience: env.MEDIAMTX_JWT_AUDIENCE,
    issuer: env.MEDIAMTX_JWT_ISSUER,
    additionalJWKS,
    privateKeyBase64: encodedKey,
  })
  tokenServiceCache = { identity, service }
  return service
}

function resolverVerifier() {
  const env = rstreamEnv()
  const jwks = env.MEDIAMTX_SOURCE_RESOLVER_JWKS ?? ""
  const identity = `${env.MEDIAMTX_SOURCE_RESOLVER_ISSUER}\u0000${env.MEDIAMTX_SOURCE_RESOLVER_AUDIENCE}\u0000${jwks}`
  if (resolverVerifierCache?.identity === identity) {
    return resolverVerifierCache.verifier
  }
  const verifier = new SourceResolverRequestVerifier({
    audience: env.MEDIAMTX_SOURCE_RESOLVER_AUDIENCE,
    issuer: env.MEDIAMTX_SOURCE_RESOLVER_ISSUER,
    jwks,
  })
  resolverVerifierCache = { identity, verifier }
  return verifier
}

function configurationIdentity(values: string[]) {
  return createHash("sha256").update(JSON.stringify(values)).digest("base64url")
}
