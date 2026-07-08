import { APP_LABEL } from "@/lib/rstream-labels"
import { createHash } from "crypto"
import { DEVICE_LABEL } from "@/lib/rstream-labels"
import { getRstreamClient } from "@/lib/rstream"
import { HTTPError } from "@/lib/error"
import { randomBytes } from "crypto"
import { randomUUID } from "crypto"
import { rstreamConfigMissingMessage } from "@/lib/env"
import { rstreamEnvResult } from "@/lib/env"
import { type Device } from "@/prisma/generated/client"
import { type DeviceView } from "@/lib/validations/device"
import { type Tunnel } from "@rstreamlabs/rstream/tunnel"
import { USER_LABEL } from "@/lib/rstream-labels"
import prisma from "@/lib/prisma"

const maxDevicesPerUser = 20
const maxDeviceCreationsPerWindow = 5
const deviceCreationWindowMs = 60 * 60 * 1000
const maxTurnCredentialsPerWindow = 20
const turnCredentialWindowMs = 60 * 1000
const turnCredentialTTLSeconds = 10 * 60

type MemoryQuota = {
  count: number
  expiresAt: number
}

declare global {
  var rstreamExampleQuota: Map<string, MemoryQuota> | undefined
}

const memoryQuota =
  globalThis.rstreamExampleQuota ?? new Map<string, MemoryQuota>()

if (!globalThis.rstreamExampleQuota) {
  globalThis.rstreamExampleQuota = memoryQuota
}

export function labels(device: Pick<Device, "id" | "userId">) {
  return {
    app: APP_LABEL,
    [DEVICE_LABEL]: device.id,
    [USER_LABEL]: device.userId,
  }
}

export function hashSecret(secret: string) {
  return createHash("sha256").update(secret).digest("hex")
}

export function createSecret() {
  return `dev_${randomBytes(32).toString("base64url")}`
}

export async function createDevice(userId: string, name: string) {
  requireMemoryQuota(
    `device:create:${userId}`,
    maxDeviceCreationsPerWindow,
    deviceCreationWindowMs,
  )
  const deviceCount = await prisma.device.count({
    where: { userId },
  })
  if (deviceCount >= maxDevicesPerUser) {
    throw new HTTPError(429, "Device limit reached.")
  }
  const id = randomUUID()
  const secret = createSecret()
  const deviceName = name.trim()
  const duplicate = await prisma.device.findFirst({
    where: { userId, name: deviceName },
    select: { id: true },
  })
  if (duplicate) {
    throw new HTTPError(409, "A device with this name already exists.")
  }
  const device = await createDeviceRecord(userId, deviceName, id, secret)
  return { device, secret }
}

function createDeviceRecord(
  userId: string,
  name: string,
  id: string,
  secret: string,
) {
  return prisma.device
    .create({
      data: {
        id,
        userId,
        name,
        secretHash: hashSecret(secret),
        secretPrefix: secret.slice(0, 12),
        tunnelName: `device-${id}`,
      },
    })
    .catch((err: unknown) => {
      if (hasPrismaCode(err, "P2002")) {
        throw new HTTPError(409, "A device with this name already exists.")
      }
      throw err
    })
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

export async function deviceViews(userId: string) {
  const devices: Device[] = await prisma.device.findMany({
    where: { userId },
    orderBy: { createdAt: "desc" },
  })
  const tunnelByDevice = new Map(await onlineTunnelEntries(userId))
  return devices.map((device) => toView(device, tunnelByDevice.has(device.id)))
}

export function toView(device: Device, online = false): DeviceView {
  return {
    id: device.id,
    name: device.name,
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
  const rstream = getRstreamClient()
  // Reuse the SDK engine resolver so device agents receive the project engine URL.
  return env.RSTREAM_ENGINE ?? rstream.getEngine()
}

// Producer tokens are scoped to one tunnel name and one device label.
export async function createTunnelToken(
  device: Pick<Device, "id" | "tunnelName" | "userId">,
) {
  const env = requireRstreamEnv()
  const rstream = getRstreamClient()
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
  device: Pick<Device, "id" | "userId">,
  tunnel: Tunnel,
) {
  const env = requireRstreamEnv()
  const rstream = getRstreamClient()
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
                labels: labels(device),
              },
              params: {
                path: { regex: "^/ws$" },
              },
            },
          },
        },
      },
    },
  })
  return token.token
}

export async function createWatchToken(userId: string) {
  const env = requireRstreamEnv()
  const rstream = getRstreamClient()
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
                labels: {
                  app: APP_LABEL,
                  [USER_LABEL]: userId,
                },
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
    memoryQuota.set(key, { count: 1, expiresAt: now + windowMs })
    return
  }
  if (current.count >= maxCount) {
    throw new HTTPError(429, "Too many requests.")
  }
  current.count += 1
}

export async function onlineTunnel(
  device: Pick<Device, "id" | "tunnelName" | "userId">,
) {
  requireRstreamEnv()
  const rstream = getRstreamClient()
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

export async function onlineTunnels(userId: string) {
  requireRstreamEnv()
  const rstream = getRstreamClient()
  // The dashboard lists only published HTTP tunnels owned by this application.
  return rstream.tunnels.list({
    limit: 100,
    filters: {
      status: "online",
      publish: true,
      protocol: "http",
      labels: {
        app: APP_LABEL,
        [USER_LABEL]: userId,
      },
    },
  })
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

export async function turnPayload(deviceId: string) {
  requireRstreamEnv()
  const rstream = getRstreamClient()
  requireMemoryQuota(
    `turn:${deviceId}`,
    maxTurnCredentialsPerWindow,
    turnCredentialWindowMs,
  )
  // TURN credentials are minted on demand and expire quickly for each viewer.
  return rstream.turn.createCredentials({
    ttlSeconds: turnCredentialTTLSeconds,
  })
}

function requireRstreamEnv() {
  const result = rstreamEnvResult()
  if (!result.success) {
    throw new HTTPError(503, rstreamConfigMissingMessage(result.error))
  }
  return result.data
}

async function onlineTunnelEntries(
  userId: string,
): Promise<[string, Tunnel][]> {
  if (!rstreamEnvResult().success) {
    return []
  }
  try {
    return (await onlineTunnels(userId)).flatMap(tunnelEntry)
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

export async function viewerPayload(device: Device) {
  const tunnel = await onlineTunnel(device)
  if (!tunnel) {
    return null
  }
  const [token, turn] = await Promise.all([
    createViewerToken(device, tunnel),
    turnPayload(device.id),
  ])
  const base = publicUrl(tunnel)
  if (!base) {
    return null
  }
  const httpBase = base.replace(/\/$/, "")
  const wsBase = httpBase.replace(/^http/, "ws")
  return {
    endpoints: {
      ws: withToken(`${wsBase}/ws`, token),
    },
    turn,
  }
}
