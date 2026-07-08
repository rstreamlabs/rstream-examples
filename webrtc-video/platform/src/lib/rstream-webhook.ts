import { APP_LABEL } from "@/lib/rstream-labels"
import { DEVICE_LABEL } from "@/lib/rstream-labels"
import { type WebhookEvent } from "@rstreamlabs/tunnels"
import prisma from "@/lib/prisma"

type DevicePresenceUpdate = {
  deviceId: string
  kind: "connected" | "disconnected"
  occurredAt: Date
}

const devicePresenceEventTypes = new Set<WebhookEvent["type"]>([
  "tunnel.created",
  "tunnel.deleted",
])

export function devicePresenceUpdateFromWebhookEvent(
  event: WebhookEvent,
): DevicePresenceUpdate | null {
  // Presence is derived from tunnel lifecycle events emitted by labelled devices.
  if (!devicePresenceEventTypes.has(event.type)) {
    return null
  }
  const occurredAt = eventCreatedAt(event)
  if (!occurredAt) {
    return null
  }
  const labels = eventLabels(event)
  if (labels?.app !== APP_LABEL) {
    return null
  }
  const deviceId = labels[DEVICE_LABEL]
  if (!deviceId) {
    return null
  }
  return {
    deviceId,
    kind: event.type === "tunnel.created" ? "connected" : "disconnected",
    occurredAt,
  }
}

export async function recordDevicePresenceFromWebhookEvent(
  event: WebhookEvent,
) {
  const update = devicePresenceUpdateFromWebhookEvent(event)
  if (!update) {
    return { deviceId: null, status: null, updated: false }
  }
  return update.kind === "connected"
    ? recordDeviceConnected(update)
    : recordDeviceDisconnected(update)
}

async function recordDeviceConnected(update: DevicePresenceUpdate) {
  // Ignore older deliveries so retries or reordering cannot move presence back.
  const result = await prisma.device.updateMany({
    where: {
      id: update.deviceId,
      AND: [
        {
          OR: [
            { onlineSince: null },
            { onlineSince: { lt: update.occurredAt } },
          ],
        },
        {
          OR: [{ lastSeenAt: null }, { lastSeenAt: { lt: update.occurredAt } }],
        },
      ],
    },
    data: {
      onlineSince: update.occurredAt,
    },
  })
  return {
    deviceId: update.deviceId,
    status: update.kind,
    updated: result.count > 0,
  }
}

async function recordDeviceDisconnected(update: DevicePresenceUpdate) {
  // A disconnect records lastSeenAt while preserving newer reconnect events.
  const result = await prisma.device.updateMany({
    where: {
      id: update.deviceId,
      AND: [
        {
          OR: [{ lastSeenAt: null }, { lastSeenAt: { lt: update.occurredAt } }],
        },
        {
          OR: [
            { onlineSince: null },
            { onlineSince: { lte: update.occurredAt } },
          ],
        },
      ],
    },
    data: {
      lastSeenAt: update.occurredAt,
      onlineSince: null,
    },
  })
  return {
    deviceId: update.deviceId,
    status: update.kind,
    updated: result.count > 0,
  }
}

function eventCreatedAt(event: WebhookEvent) {
  if (!event.created_at) {
    return null
  }
  const date = new Date(event.created_at)
  return Number.isNaN(date.getTime()) ? null : date
}

function eventLabels(event: WebhookEvent): Record<string, string> | null {
  return stringRecord(recordValue(event.object, "labels"))
}

function recordValue(value: unknown, key: string): unknown {
  return isRecord(value) ? value[key] : undefined
}

function stringRecord(value: unknown): Record<string, string> | null {
  return isRecord(value) ? Object.fromEntries(stringEntries(value)) : null
}

function stringEntries(value: Record<string, unknown>): [string, string][] {
  return Object.entries(value).flatMap(([key, item]) =>
    typeof item === "string" ? [[key, item]] : [],
  )
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
