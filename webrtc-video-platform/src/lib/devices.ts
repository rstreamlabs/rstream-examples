import { createHash, randomBytes, randomUUID } from "crypto"
import { HTTPError } from "@/lib/error"
import { type Device } from "@/prisma/generated/client"
import { type DeviceView } from "@/lib/validations/device"
import { type Tunnel } from "@rstreamlabs/tunnels"

import { APP_LABEL } from "@/lib/rstream-labels"
import { DEVICE_LABEL } from "@/lib/rstream-labels"
import { USER_LABEL } from "@/lib/rstream-labels"
import { rstreamEnv } from "@/lib/env"
import prisma from "@/lib/prisma"
import rstream from "@/lib/rstream"

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
  let device: Device
  try {
    device = await prisma.device.create({
      data: {
        id,
        userId,
        name: deviceName,
        secretHash: hashSecret(secret),
        secretPrefix: secret.slice(0, 12),
        tunnelName: `device-${id}`,
      },
    })
  } catch (err) {
    if (hasPrismaCode(err, "P2002")) {
      throw new HTTPError(409, "A device with this name already exists.")
    }
    throw err
  }
  return { device, secret }
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

export async function touchDevice(deviceId: string) {
  await prisma.device.update({
    where: { id: deviceId },
    data: { lastSeenAt: new Date() },
  })
}

function tunnelEntry(tunnel: Tunnel): [string, Tunnel][] {
  const deviceId = tunnel.labels?.[DEVICE_LABEL]
  return deviceId ? [[deviceId, tunnel]] : []
}

export async function deviceViews(userId: string) {
  const devices = await prisma.device.findMany({
    where: { userId },
    orderBy: { createdAt: "desc" },
  })
  const tunnelByDevice = new Map(
    (await onlineTunnels(userId)).flatMap(tunnelEntry),
  )
  return devices.map((device) => toView(device, tunnelByDevice.has(device.id)))
}

export function toView(device: Device, online = false): DeviceView {
  return {
    id: device.id,
    name: device.name,
    secretPrefix: device.secretPrefix,
    tunnelName: device.tunnelName,
    online,
    lastSeenAt: device.lastSeenAt?.toISOString() ?? null,
    createdAt: device.createdAt.toISOString(),
  }
}

export async function engine() {
  return rstreamEnv().RSTREAM_ENGINE ?? rstream.getEngine()
}

export async function createTunnelToken(
  device: Pick<Device, "id" | "tunnelName" | "userId">,
) {
  const env = rstreamEnv()
  if (!env.RSTREAM_FINE_GRAINED_GRANTS) {
    // Basic-plan fallback: short-lived token, but no tunnel-level grant.
    const token = await rstream.auth.createAuthToken({
      expires_in: env.DEVICE_TOKEN_TTL_SECONDS,
    })
    return token.token
  }
  // Producer tokens are scoped to one tunnel name and one device label.
  const token = await rstream.auth.createAuthToken({
    expires_in: env.DEVICE_TOKEN_TTL_SECONDS,
    tunnelsGrants: [
      {
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
    ],
  })
  return token.token
}

export async function createViewerToken(
  device: Pick<Device, "id" | "userId">,
  tunnel: Tunnel,
) {
  const env = rstreamEnv()
  if (!env.RSTREAM_FINE_GRAINED_GRANTS) {
    // Basic-plan fallback: short-lived token, but no tunnel/path isolation.
    const token = await rstream.auth.createAuthToken({
      expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    })
    return token.token
  }
  // Viewer tokens can only connect to the selected online tunnel WebRTC path.
  const token = await rstream.auth.createAuthToken({
    expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    tunnelsGrants: [
      {
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
    ],
  })
  return token.token
}

export async function createWatchToken(userId: string) {
  const env = rstreamEnv()
  if (!env.RSTREAM_FINE_GRAINED_GRANTS) {
    // Basic-plan fallback: short-lived token, but no server-side list filter.
    const token = await rstream.auth.createAuthToken({
      expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    })
    return token.token
  }
  // Watch tokens only list published sample tunnels for the dashboard state.
  const token = await rstream.auth.createAuthToken({
    expires_in: env.VIEWER_TOKEN_TTL_SECONDS,
    tunnelsGrants: [
      {
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
    ],
  })
  return token.token
}

export async function onlineTunnel(device: Pick<Device, "id" | "userId">) {
  const activeTunnels = await rstream.tunnels.list({
    limit: 20,
    filters: {
      status: "online",
      publish: true,
      protocol: "http",
      labels: labels(device),
    },
  })
  return activeTunnels[0] ?? null
}

export async function onlineTunnels(userId: string) {
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

export async function tunnelPayload(device: Device) {
  const env = rstreamEnv()
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

export async function turnPayload() {
  return rstream.turn.createCredentials()
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
    rstream.turn.createCredentials(),
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
