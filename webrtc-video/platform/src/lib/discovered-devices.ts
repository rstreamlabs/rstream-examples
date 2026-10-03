import "server-only"
import {
  deviceInventoryConfig,
  discoveredSources,
  discoveryLabels,
  discoveryDeviceID,
} from "@/lib/device-inventory"
import { HTTPError } from "@/lib/error"
import { getRstreamClient } from "@/lib/rstream"
import prisma from "@/lib/prisma"
import { type DeviceView } from "@/lib/validations/device"

const maximumLiveDevices = 100

export async function discoverDevices(id?: string, signal?: AbortSignal) {
  const config = deviceInventoryConfig()
  if (config.mode !== "discovered")
    throw new HTTPError(404, "Discovery is disabled.")
  if (id && !discoveryDeviceID.safeParse(id).success)
    throw new HTTPError(404, "Device not found.")
  const observedAt = new Date()
  try {
    const client = await getRstreamClient(signal)
    const tunnels = await client.tunnels.list({
      limit: id ? 3 : maximumLiveDevices + 1,
      filters: {
        status: "online",
        publish: true,
        protocol: "http",
        labels: { ...discoveryLabels, ...(id ? { device: id } : {}) },
      },
    })
    if (!id && tunnels.length > maximumLiveDevices)
      throw new HTTPError(
        503,
        "Discovered inventory exceeds the 100 live tunnel limit.",
      )
    const inventory = discoveredSources(tunnels, config.projectId)
    if (inventory.conflicts.size)
      throw new HTTPError(
        409,
        "Multiple video tunnels advertise the same device ID. Give each producer a unique stable UUID.",
      )
    // Never accept a mislabeled response from an upstream or test double.
    if (id)
      for (const key of inventory.sources.keys())
        if (key !== id) inventory.sources.delete(key)
    return { ...inventory, observedAt, config }
  } catch (error) {
    if (error instanceof HTTPError) throw error
    if (signal?.aborted) throw new HTTPError(499, "Request cancelled.")
    throw new HTTPError(503, "Device discovery is unavailable. Retry shortly.")
  }
}

export async function discoveredDeviceViews(
  signal?: AbortSignal,
): Promise<DeviceView[]> {
  const { sources, observedAt, config } = await discoverDevices(
    undefined,
    signal,
  )
  const live = [...sources.values()].map((source): DeviceView => ({
    id: source.id,
    name: source.name,
    inventory: "discovered",
    secretPrefix: null,
    tunnelName: source.tunnelName,
    online: true,
    onlineSince: null,
    lastSeenAt: observedAt.toISOString(),
    createdAt: observedAt.toISOString(),
  }))
  if (!config.remember) return live
  if (live.length) {
    // One atomic statement: concurrent polls cannot regress names or last-seen
    // timestamps. Snapshot start time precedes the network read, not its finish.
    const rows = JSON.stringify(
      live.map((device) => ({
        id: device.id,
        name: device.name,
        tunnelName: device.tunnelName,
      })),
    )
    try {
      await prisma.$transaction(async (transaction) => {
        await transaction.$executeRaw`SET LOCAL lock_timeout = '3s'`
        const lockKey = `discovery:${config.projectId}`
        await transaction.$queryRaw`SELECT pg_advisory_xact_lock(hashtextextended(${lockKey}, 0))::text`
        const existing = await transaction.discoveredDevice.findMany({
          where: { projectId: config.projectId },
          select: { deviceId: true },
          take: 1001,
        })
        const ids = new Set(existing.map((device) => device.deviceId))
        for (const source of live) ids.add(source.id)
        if (ids.size > 1000)
          throw new HTTPError(
            503,
            "Discovered history exceeds 1000 devices. Archive obsolete history or disable discovery history.",
          )
        signal?.throwIfAborted()
        await transaction.$executeRaw`
      INSERT INTO discovered_devices ("projectId", "deviceId", name, "tunnelName", "firstSeenAt", "lastSeenAt")
      SELECT ${config.projectId}, id, name, "tunnelName", ${observedAt}, ${observedAt}
      FROM jsonb_to_recordset(${rows}::jsonb) AS incoming(id text, name text, "tunnelName" text)
      ON CONFLICT ("projectId", "deviceId") DO UPDATE
      SET name = EXCLUDED.name, "tunnelName" = EXCLUDED."tunnelName", "lastSeenAt" = EXCLUDED."lastSeenAt"
      WHERE discovered_devices."lastSeenAt" < EXCLUDED."lastSeenAt"`
      })
    } catch (error) {
      if (error instanceof HTTPError) throw error
      if (signal?.aborted) throw new HTTPError(499, "Request cancelled.")
      throw new HTTPError(503, "Device history is unavailable. Retry shortly.")
    }
  }
  const remembered = await prisma.discoveredDevice.findMany({
    where: { projectId: config.projectId },
    orderBy: { lastSeenAt: "desc" },
    take: 1001,
  })
  if (remembered.length > 1000)
    throw new HTTPError(
      503,
      "Discovered history exceeds 1000 devices. Archive obsolete history or disable discovery history.",
    )
  return remembered.map((device): DeviceView => ({
    id: device.deviceId,
    name: device.name,
    inventory: "discovered",
    secretPrefix: null,
    tunnelName: device.tunnelName,
    online: sources.has(device.deviceId),
    onlineSince: null,
    lastSeenAt: device.lastSeenAt.toISOString(),
    createdAt: device.firstSeenAt.toISOString(),
  }))
}
