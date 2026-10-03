import "server-only"

import { MediaMTXMetricsReader } from "./mediamtx-metrics"
import { MediaMTXPlayback } from "./mediamtx-playback"

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

let metricsReaderCache:
  { endpoint: string; reader: MediaMTXMetricsReader } | undefined

let playbackCache: { identity: string; client: MediaMTXPlayback } | undefined

export function mediaMTXPlayback() {
  const env = rstreamEnv()
  const endpoint = env.MEDIAMTX_PLAYBACK_URL
  if (env.VIDEO_DISTRIBUTOR !== "mediamtx" || !endpoint) return null
  const identity = JSON.stringify([
    endpoint,
    env.MEDIAMTX_RECORDING_WINDOW_SECONDS,
  ])
  if (playbackCache?.identity !== identity) {
    playbackCache = {
      identity,
      client: new MediaMTXPlayback({
        endpoint,
        windowSeconds: env.MEDIAMTX_RECORDING_WINDOW_SECONDS,
        credential: (path) =>
          tokenService().sign({
            action: "playback",
            path,
            subject: "platform-playback",
            ttlSeconds: 45,
          }),
      }),
    }
  }
  return playbackCache.client
}

export async function mediaMTXMetrics(deviceID: string, signal: AbortSignal) {
  const env = rstreamEnv()
  const endpoint = env.MEDIAMTX_METRICS_URL
  if (env.VIDEO_DISTRIBUTOR !== "mediamtx" || !endpoint) return null
  if (metricsReaderCache?.endpoint !== endpoint) {
    metricsReaderCache = {
      endpoint,
      reader: new MediaMTXMetricsReader({ endpoint }),
    }
  }
  return metricsReaderCache.reader.read(mediaPath(deviceID), signal)
}

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
