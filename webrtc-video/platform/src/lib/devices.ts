import {
  deviceInventoryConfig,
  discoveryLabels,
  type DiscoveredSource,
} from "@/lib/device-inventory"
import {
  discoverDevices,
  discoveredDeviceViews,
} from "@/lib/discovered-devices"
import { APP_LABEL } from "@/lib/rstream-labels"
import { createHash } from "crypto"
import { credentialExpiresAt } from "@/lib/video-distributor-token"
import { DEVICE_LABEL } from "@/lib/rstream-labels"
import { getRstreamClient } from "@/lib/rstream"
import { HTTPError } from "@/lib/error"
import { randomBytes } from "crypto"
import { randomUUID } from "crypto"
import { rstreamConfigMissingMessage } from "@/lib/env"
import { rstreamEnvResult } from "@/lib/env"
import { type Device } from "@/prisma/generated/client"
import { type DeviceView } from "@/lib/validations/device"
import { type ViewerDistributionPreference } from "@/lib/validations/device"
import { type MediaMTXSourcePurpose } from "@/lib/validations/video-distributor"
import { type Tunnel } from "@rstreamlabs/rstream/tunnel"
import {
  deviceOwner,
  deviceOwnerLabels,
  deviceOwnerWhere,
  type DeviceAccess,
} from "@/lib/device-access"
import { issueSourceCredentials } from "@/lib/video-source-resolution"
import { mediaMTXPublisherCredential } from "@/lib/video-distributor"
import { mediaMTXViewerCredential } from "@/lib/video-distributor"
import { mediaMTXPath } from "@/lib/video-distributor"
import { mediaMTXWHEPURL } from "@/lib/video-distributor-endpoint"
import { videoDistributorMode } from "@/lib/video-distributor"
import prisma from "@/lib/prisma"
import { boundedFetch } from "@/lib/bounded-rstream"

type SourceDevice =
  | Pick<Device, "id" | "tunnelName" | "userId" | "organizationId">
  | DiscoveredSource

type SourceIdentity =
  Pick<Device, "id" | "userId" | "organizationId"> | DiscoveredSource

export function requireManagedInventory() {
  if (deviceInventoryConfig().mode !== "managed")
    throw new HTTPError(
      409,
      "Devices are discovered from the configured rstream project. Provisioning and deletion are disabled.",
    )
}

export async function findSourceDevice(
  id: string,
  access?: DeviceAccess,
  signal?: AbortSignal,
) {
  if (deviceInventoryConfig().mode === "discovered") {
    if (access && access.kind !== "organization")
      throw new HTTPError(403, "Organization access required.")
    return (await discoverDevices(id, signal)).sources.get(id) ?? null
  }
  return prisma.device.findFirst({
    where: { id, ...(access ? deviceOwnerWhere(access) : {}) },
  })
}

const maxDevicesPerOwner = 20
const maxDeviceCreationsPerWindow = 5
const deviceCreationWindowMs = 60 * 60 * 1000
const maxTurnCredentialsPerWindow = 20
const turnCredentialWindowMs = 60 * 1000

type MemoryQuota = {
  count: number
  expiresAt: number
}

type ExpiringConnectToken = {
  expiresAt: Date
  token: string
}

declare global {
  var rstreamExampleQuota: Map<string, MemoryQuota> | undefined
}

const memoryQuota =
  globalThis.rstreamExampleQuota ?? new Map<string, MemoryQuota>()

if (!globalThis.rstreamExampleQuota) {
  globalThis.rstreamExampleQuota = memoryQuota
}

export function labels(device: SourceIdentity) {
  if ("inventory" in device)
    return { ...discoveryLabels, [DEVICE_LABEL]: device.id }
  return {
    app: APP_LABEL,
    [DEVICE_LABEL]: device.id,
    ...deviceOwnerLabels(deviceOwner(device)),
  }
}

export function hashSecret(secret: string) {
  return createHash("sha256").update(secret).digest("hex")
}

export function createSecret() {
  return `dev_${randomBytes(32).toString("base64url")}`
}

export async function createDevice(
  access: DeviceAccess,
  createdById: string,
  name: string,
) {
  requireManagedInventory()
  requireMemoryQuota(
    `device:create:${createdById}`,
    maxDeviceCreationsPerWindow,
    deviceCreationWindowMs,
  )
  const id = randomUUID()
  const secret = createSecret()
  const deviceName = name.trim()
  const owner = deviceOwnerWhere(access)
  try {
    const device = await prisma.$transaction(async (transaction) => {
      // Serialize inventory limits across processes and users of the same owner.
      await transaction.$executeRaw`SET LOCAL lock_timeout = '3s'`
      const lockKey = `${access.kind}:${access.id}`
      await transaction.$queryRaw`SELECT pg_advisory_xact_lock(hashtextextended(${lockKey}, 0))::text`
      const deviceCount = await transaction.device.count({ where: owner })
      if (deviceCount >= maxDevicesPerOwner)
        throw new HTTPError(429, "Device limit reached.")
      return transaction.device.create({
        data: {
          id,
          ...owner,
          createdById,
          name: deviceName,
          secretHash: hashSecret(secret),
          secretPrefix: secret.slice(0, 12),
          tunnelName: `device-${id}`,
        },
      })
    })
    return { device, secret }
  } catch (error) {
    if (hasPrismaCode(error, "P2002"))
      throw new HTTPError(409, "A device with this name already exists.")
    if (hasPrismaCode(error, "P2028") || isDatabaseLockTimeout(error))
      throw new HTTPError(503, "Device inventory is busy. Retry shortly.")
    throw error
  }
}

function isDatabaseLockTimeout(error: unknown) {
  if (
    !hasPrismaCode(error, "P2010") ||
    !error ||
    typeof error !== "object" ||
    !("meta" in error)
  )
    return false
  const meta = error.meta
  if (!meta || typeof meta !== "object") return false
  if ("code" in meta && meta.code === "55P03") return true
  const adapter = "driverAdapterError" in meta ? meta.driverAdapterError : null
  if (!adapter || typeof adapter !== "object" || !("cause" in adapter))
    return false
  const cause = adapter.cause
  return (
    !!cause &&
    typeof cause === "object" &&
    "originalCode" in cause &&
    cause.originalCode === "55P03"
  )
}

function hasPrismaCode(err: unknown, code: string) {
  return (
    typeof err === "object" &&
    err !== null &&
    "code" in err &&
    err.code === code
  )
}

export async function deviceBySecret(secret: string) {
  return prisma.device.findUnique({
    where: {
      secretHash: hashSecret(secret),
    },
  })
}

function bearerSecret(request: Request) {
  const authorization = request.headers.get("authorization") ?? ""
  const token = authorization.match(/^Bearer\s+(\S+)$/i)?.[1]
  if (!token) {
    throw new HTTPError(401, "Unauthorized")
  }
  return token
}

export async function requireDevice(request: Request) {
  requireManagedInventory()
  const device = await deviceBySecret(bearerSecret(request))
  if (!device) {
    throw new HTTPError(401, "Unauthorized")
  }
  return device
}

function tunnelEntry(tunnel: Tunnel): [string, Tunnel][] {
  const deviceId = tunnel.labels?.[DEVICE_LABEL]
  return deviceId ? [[deviceId, tunnel]] : []
}

export async function deviceViews(access: DeviceAccess, signal?: AbortSignal) {
  if (deviceInventoryConfig().mode === "discovered") {
    if (access.kind !== "organization")
      throw new HTTPError(403, "Organization access required.")
    return discoveredDeviceViews(signal)
  }
  const devices: Device[] = await prisma.device.findMany({
    where: deviceOwnerWhere(access),
    orderBy: { createdAt: "desc" },
  })
  const tunnelByDevice = new Map(await onlineTunnelEntries(access))
  return devices.map((device) => toView(device, tunnelByDevice.has(device.id)))
}

export function toView(device: Device, online = false): DeviceView {
  return {
    id: device.id,
    name: device.name,
    inventory: "managed",
    secretPrefix: device.secretPrefix,
    tunnelName: device.tunnelName,
    online,
    onlineSince: device.onlineSince?.toISOString() ?? null,
    lastSeenAt: device.lastSeenAt?.toISOString() ?? null,
    createdAt: device.createdAt.toISOString(),
  }
}

export async function engine() {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient()
  // Reuse the SDK engine resolver so device agents receive the project engine URL.
  return env.RSTREAM_ENGINE ?? rstream.getEngine()
}

// Producer tokens are scoped to one tunnel name and one device label.
export async function createTunnelToken(device: SourceDevice) {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient()
  const token = await rstream.auth.createAuthToken({
    expires_in: env.DEVICE_TOKEN_TTL_SECONDS,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            create: {
              filters: {
                name: { exact: device.tunnelName },
                protocol: "http",
                publish: true,
                token_auth: true,
                labels: labels(device),
              },
            },
          },
        },
      },
    },
  })
  return token.token
}

// Viewer tokens can only connect to the selected online tunnel WebRTC path.
export async function createViewerToken(
  device: SourceIdentity,
  tunnel: Tunnel,
  signal?: AbortSignal,
) {
  return createDeviceConnectToken(
    device,
    tunnel,
    "^/whep(?:/[^/?#]{1,256})?$",
    signal,
  )
}

export async function createWHEPSourceToken(
  device: SourceIdentity,
  tunnel: Tunnel,
  signal?: AbortSignal,
) {
  return createDeviceConnectToken(
    device,
    tunnel,
    "^/whep(?:/[^/?#]{1,256})?$",
    signal,
  )
}

async function createDeviceConnectToken(
  device: SourceIdentity,
  tunnel: Tunnel,
  pathRegex: string,
  signal?: AbortSignal,
) {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient(signal)
  const issuedAt = new Date()
  const token = await rstream.auth.createAuthToken({
    expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            connect: {
              filters: {
                id: tunnel.id,
                status: "online",
                protocol: "http",
                publish: true,
                token_auth: true,
                labels: labels(device),
              },
              params: {
                path: { regex: pathRegex },
              },
            },
          },
        },
      },
    },
  })
  return {
    expiresAt: credentialExpiresAt(issuedAt, env.VIEWER_TOKEN_TTL_SECONDS),
    token: token.token,
  } satisfies ExpiringConnectToken
}

async function createMediaMTXConnectToken(
  tunnel: Tunnel,
  path: string,
  signal?: AbortSignal,
) {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient(signal)
  const escapedPath = escapeRegex(path)
  const issuedAt = new Date()
  const token = await rstream.auth.createAuthToken({
    expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            connect: {
              filters: {
                id: tunnel.id,
                status: "online",
                protocol: "http",
                publish: true,
                token_auth: true,
              },
              params: {
                path: {
                  regex: `^/${escapedPath}/whep(?:/[^/?#]{1,256})?$`,
                },
              },
            },
          },
        },
      },
    },
  })
  return {
    expiresAt: credentialExpiresAt(issuedAt, env.VIEWER_TOKEN_TTL_SECONDS),
    token: token.token,
  } satisfies ExpiringConnectToken
}

export async function createWatchToken(access: DeviceAccess) {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient()
  // Watch tokens are short-lived because the browser sends them as query tokens.
  const token = await rstream.auth.createAuthToken({
    expires_in: env.WATCH_TOKEN_TTL_SECONDS,
    permissions: ["tunnels.resources.read-only"],
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            list: {
              filters: {
                labels:
                  deviceInventoryConfig().mode === "discovered"
                    ? discoveryLabels
                    : { app: APP_LABEL, ...deviceOwnerLabels(access) },
                protocol: "http",
                publish: true,
              },
              select: {
                id: true,
                status: true,
                name: true,
                protocol: true,
                publish: true,
                labels: true,
                host: true,
                hostname: true,
                client_id: true,
              },
            },
          },
        },
      },
    },
  })
  return token.token
}

function requireMemoryQuota(key: string, maxCount: number, windowMs: number) {
  const now = Date.now()
  const current = memoryQuota.get(key)
  if (!current || current.expiresAt <= now) {
    for (const [quotaKey, quota] of memoryQuota) {
      if (quota.expiresAt <= now) memoryQuota.delete(quotaKey)
    }
    if (memoryQuota.size >= 4096)
      throw new HTTPError(503, "Request quota capacity reached. Retry shortly.")
    memoryQuota.set(key, { count: 1, expiresAt: now + windowMs })
    return
  }
  if (current.count >= maxCount) {
    throw new HTTPError(429, "Too many requests.")
  }
  current.count += 1
}

async function onlineTunnel(device: SourceDevice, signal?: AbortSignal) {
  signal?.throwIfAborted()
  // Every caller received this discovered source from findSourceDevice in the
  // current request. Reuse that live, label-validated snapshot; history is never
  // a source of authorization. Issued connect tokens still require this exact
  // tunnel ID, online status and labels when used at the edge.
  if ("inventory" in device) return device.tunnel
  requireRstreamEnv()
  const rstream = await getRstreamClient(signal)
  // Online state is read from rstream inventory and narrowed by stable labels.
  const activeTunnels = await rstream.tunnels.list({
    limit: 20,
    filters: {
      name: device.tunnelName,
      status: "online",
      publish: true,
      protocol: "http",
      labels: labels(device),
    },
  })
  return newestTunnel(activeTunnels)
}

export async function onlineTunnels(access: DeviceAccess) {
  requireRstreamEnv()
  const rstream = await getRstreamClient()
  // The dashboard lists only published HTTP tunnels owned by this application.
  return rstream.tunnels.list({
    limit: 100,
    filters: {
      status: "online",
      publish: true,
      protocol: "http",
      labels: {
        app: APP_LABEL,
        ...deviceOwnerLabels(access),
      },
    },
  })
}

export async function onlineMediaMTXTunnel(signal?: AbortSignal) {
  const env = requireRstreamEnv()
  if (
    env.VIDEO_DISTRIBUTOR !== "mediamtx" ||
    env.MEDIAMTX_EXPOSURE !== "rstream"
  ) {
    return null
  }
  const rstream = await getRstreamClient(signal)
  const activeTunnels = await rstream.tunnels.list({
    limit: 20,
    filters: {
      name: env.MEDIAMTX_TUNNEL_NAME,
      status: "online",
      publish: true,
      protocol: "http",
    },
  })
  return newestTunnel(activeTunnels)
}

function newestTunnel(tunnels: Tunnel[]) {
  return (
    [...tunnels].sort((left, right) => {
      return tunnelTimestamp(right) - tunnelTimestamp(left)
    })[0] ?? null
  )
}

function tunnelTimestamp(tunnel: Tunnel) {
  const value = tunnel.creation_date
  if (!value) {
    return 0
  }
  const timestamp = new Date(value).getTime()
  return Number.isFinite(timestamp) ? timestamp : 0
}

export async function tunnelPayload(device: Device) {
  const env = requireRstreamEnv()
  const [resolvedEngine, token] = await Promise.all([
    engine(),
    createTunnelToken(device),
  ])
  return {
    device: device.id,
    engine: resolvedEngine,
    token,
    name: device.tunnelName,
    labels: labels(device),
    expires: new Date(
      Date.now() + env.DEVICE_TOKEN_TTL_SECONDS * 1000,
    ).toISOString(),
  }
}

export async function turnPayload(deviceId: string, signal?: AbortSignal) {
  const env = requireRstreamEnv()
  const rstream = await getRstreamClient(signal)
  requireMemoryQuota(
    `turn:${deviceId}`,
    maxTurnCredentialsPerWindow,
    turnCredentialWindowMs,
  )
  // TURN credentials are minted on demand and expire quickly for each viewer.
  const issuedAt = new Date()
  const target = await rstream.getTURNTarget()
  const keyring = new URL(
    `/keyrings/turn/${encodeURIComponent(target.turnRealm)}.spki.der.hex`,
    env.RSTREAM_TURN_KEYRING_BASE_URL ??
      env.RSTREAM_API_URL ??
      "https://rstream.io",
  )
  const keyResponse = await boundedFetch(signal, 16 * 1024)(keyring)
  if (!keyResponse.ok) throw new HTTPError(503, "TURN keyring is unavailable")
  const credentials = await rstream.turn.createCredentials({
    projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
    serverPublicKeyHex: (await keyResponse.text()).trim(),
    ...target,
    ttlSeconds: env.TURN_CREDENTIAL_TTL_SECONDS,
  })
  return {
    ...credentials,
    expiresAt: credentialExpiresAt(issuedAt, credentials.ttl).toISOString(),
  }
}

function requireRstreamEnv() {
  const result = rstreamEnvResult()
  if (!result.success) {
    throw new HTTPError(503, rstreamConfigMissingMessage(result.error))
  }
  return result.data
}

async function onlineTunnelEntries(
  access: DeviceAccess,
): Promise<[string, Tunnel][]> {
  if (!rstreamEnvResult().success) {
    return []
  }
  try {
    return (await onlineTunnels(access)).flatMap(tunnelEntry)
  } catch {
    return []
  }
}

function publicUrl(tunnel: Tunnel) {
  const host = tunnel.host ?? tunnel.hostname
  if (host) {
    return `https://${host}`
  }
  return null
}

function withToken(rawUrl: string, token: string) {
  const url = new URL(rawUrl)
  url.searchParams.set("rstream.token", token)
  return url.toString()
}

export async function viewerPayload(
  device: SourceDevice,
  distribution: ViewerDistributionPreference = "automatic",
  signal?: AbortSignal,
) {
  const allowDirectFallback = requireRstreamEnv().MEDIAMTX_ALLOW_DIRECT_FALLBACK
  if (
    distribution === "direct" &&
    videoDistributorMode() === "mediamtx" &&
    !allowDirectFallback
  ) {
    throw new HTTPError(403, "This deployment requires MediaMTX playback.")
  }
  if (distribution === "automatic" && videoDistributorMode() === "mediamtx") {
    const distributed = await mediaMTXViewerPayload(device, signal)
    if (distributed) return { ...distributed, allowDirectFallback }
    if (!allowDirectFallback)
      throw new HTTPError(
        503,
        "MediaMTX is unavailable. Direct fallback is disabled.",
      )
  }
  const direct = await directViewerPayload(device, signal)
  return direct ? { ...direct, allowDirectFallback } : null
}

async function directViewerPayload(device: SourceDevice, signal?: AbortSignal) {
  const tunnel = await onlineTunnel(device, signal)
  if (!tunnel) {
    return null
  }
  const [credential, turn] = await Promise.all([
    createViewerToken(device, tunnel, signal),
    turnPayload(device.id, signal),
  ])
  const base = publicUrl(tunnel)
  if (!base) {
    return null
  }
  return {
    distributor: {
      kind: "direct" as const,
      whep: withToken(`${base.replace(/\/$/, "")}/whep`, credential.token),
      authorization: "",
      expiresAt: credential.expiresAt.toISOString(),
    },
    turn,
  }
}

async function mediaMTXViewerPayload(
  device: SourceDevice,
  signal?: AbortSignal,
) {
  const endpoint = await mediaMTXViewerEndpoint(signal)
  if (!endpoint) {
    return null
  }
  const path = mediaMTXPath(device.id)
  const [accessCredential, mediaCredential, turn] = await Promise.all([
    endpoint.kind === "rstream"
      ? createMediaMTXConnectToken(endpoint.tunnel, path, signal)
      : Promise.resolve(null),
    Promise.resolve(mediaMTXViewerCredential(device.id)),
    turnPayload(device.id, signal),
  ])
  const expiresAt = accessCredential
    ? earliestDate(accessCredential.expiresAt, mediaCredential.expiresAt)
    : mediaCredential.expiresAt
  return {
    distributor: {
      kind: "mediamtx" as const,
      whep: mediaMTXWHEPURL(endpoint.baseURL, path, accessCredential?.token),
      authorization: `Bearer ${mediaCredential.token}`,
      expiresAt: expiresAt.toISOString(),
    },
    turn,
  }
}

async function mediaMTXViewerEndpoint(signal?: AbortSignal) {
  const env = requireRstreamEnv()
  if (env.MEDIAMTX_EXPOSURE === "public") {
    return {
      baseURL: env.MEDIAMTX_PUBLIC_URL ?? "",
      kind: "public" as const,
    }
  }
  const tunnel = await onlineMediaMTXTunnel(signal)
  if (!tunnel) {
    return null
  }
  const baseURL = publicUrl(tunnel)
  return baseURL ? { baseURL, kind: "rstream" as const, tunnel } : null
}

export async function mediaMTXSourcePayload(
  device: SourceDevice,
  purpose: MediaMTXSourcePurpose,
  signal?: AbortSignal,
) {
  const tunnel = await onlineTunnel(device, signal)
  if (!tunnel) {
    return null
  }
  const credentials = await issueSourceCredentials(purpose, {
    issueSource: () => createWHEPSourceToken(device, tunnel, signal),
    issueDestination: async () => mediaMTXPublisherCredential(device.id),
    issueTURN: () => turnPayload(device.id, signal),
  })
  const base = publicUrl(tunnel)
  if (!base) {
    return null
  }
  const expiresAt = earliestDate(
    credentials.source.expiresAt,
    credentials.destination.expiresAt,
  )
  return {
    url: withToken(`${base.replace(/\/$/, "")}/whep`, credentials.source.token),
    authorization: "",
    destinationAuthorization: `Bearer ${credentials.destination.token}`,
    iceServers: credentials.turn
      ? [
          {
            urls: credentials.turn.urls,
            username: credentials.turn.username,
            credential: credentials.turn.credential,
            expiresAt: credentials.turn.expiresAt,
          },
        ]
      : [],
    expiresAt: expiresAt.toISOString(),
  }
}

function escapeRegex(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

function earliestDate(left: Date, right: Date) {
  return left < right ? left : right
}

// Control credentials never leave the platform. They cannot reach WHEP, and
// viewer/source credentials cannot reach this endpoint.
export async function qualityEndpoint(
  device: SourceDevice,
  signal?: AbortSignal,
) {
  const tunnel = await onlineTunnel(device, signal)
  if (!tunnel) throw new HTTPError(409, "Device is offline")
  const base = publicUrl(tunnel)
  if (!base) throw new HTTPError(503, "Device control is unavailable")
  const credential = await createDeviceConnectToken(
    device,
    tunnel,
    "^/api/quality$",
    signal,
  )
  return withToken(`${base}/api/quality`, credential.token)
}
