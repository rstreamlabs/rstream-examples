import { z } from "zod"
import { type Tunnel } from "@rstreamlabs/rstream/tunnel"

export const discoveryLabels = {
  app: "webrtc-video-platform",
  inventory: "discovered",
} as const
export const deviceNameLabel = "device-name"
export const discoveryDeviceID = z
  .string()
  .regex(
    /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
  )

const inventorySchema = z
  .object({
    DEVICE_INVENTORY_MODE: z.enum(["managed", "discovered"]).default("managed"),
    DEVICE_DISCOVERY_HISTORY_ENABLED: z.enum(["true", "false"]).default("true"),
    DEVICE_ACCESS_MODE: z.enum(["user", "organization"]).default("user"),
    RSTREAM_PROJECT_ID: z.string().trim().default(""),
  })
  .superRefine((env, ctx) => {
    if (env.DEVICE_INVENTORY_MODE !== "discovered") return
    if (env.DEVICE_ACCESS_MODE !== "organization")
      ctx.addIssue({
        code: "custom",
        message: "Discovered inventory requires organization access.",
      })
    if (!env.RSTREAM_PROJECT_ID)
      ctx.addIssue({
        code: "custom",
        message: "RSTREAM_PROJECT_ID is required for discovered inventory.",
      })
  })

export function deviceInventoryConfig(environment = process.env) {
  const env = inventorySchema.parse(environment)
  return {
    mode: env.DEVICE_INVENTORY_MODE,
    remember: env.DEVICE_DISCOVERY_HISTORY_ENABLED === "true",
    projectId: env.RSTREAM_PROJECT_ID,
  }
}

export type DiscoveredSource = {
  inventory: "discovered"
  id: string
  name: string
  projectId: string
  tunnelName: string
  tunnel: Tunnel
}

// Labels describe a source; they never grant access outside the configured
// project. Recheck the returned properties even when the engine filters them.
export function discoveredSource(
  tunnel: Tunnel,
  projectId: string,
): DiscoveredSource | null {
  const labels = tunnel.labels
  const id = discoveryDeviceID.safeParse(labels?.device)
  if (
    !id.success ||
    tunnel.project_id !== projectId ||
    tunnel.status !== "online" ||
    tunnel.protocol !== "http" ||
    tunnel.publish !== true ||
    tunnel.token_auth !== true ||
    tunnel.rstream_auth === true ||
    labels?.app !== discoveryLabels.app ||
    labels?.inventory !== discoveryLabels.inventory
  )
    return null
  const rawName = labels[deviceNameLabel]
  const name = rawName?.trim()
  // Invalid metadata is rejected instead of silently naming a different device.
  if (
    rawName !== undefined &&
    (!name ||
      Buffer.byteLength(name, "utf8") > 80 ||
      /[\p{Cc}\p{Cf}]/u.test(rawName))
  )
    return null
  const host = tunnel.host ?? tunnel.hostname
  if (
    !host ||
    host.length > 253 ||
    !/^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z][a-z0-9-]*$/i.test(host)
  )
    return null
  return {
    inventory: "discovered",
    id: id.data,
    name: name ?? `Device ${id.data.slice(0, 8)}`,
    projectId,
    tunnelName: tunnel.name || `device-${id.data}`,
    tunnel,
  }
}

export function discoveredSources(tunnels: Tunnel[], projectId: string) {
  const sources = new Map<string, DiscoveredSource>()
  const conflicts = new Set<string>()
  for (const tunnel of tunnels) {
    const source = discoveredSource(tunnel, projectId)
    if (!source) continue
    if (sources.has(source.id)) conflicts.add(source.id)
    else sources.set(source.id, source)
  }
  for (const id of conflicts) sources.delete(id)
  return { sources, conflicts }
}
