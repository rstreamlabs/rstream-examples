import { z } from "zod"

// Scope is derived exclusively from the authenticated identity, never a request.
export type DeviceAccess =
  { kind: "user"; id: string } | { kind: "organization"; id: string }

const accessConfigSchema = z
  .object({
    DEVICE_ACCESS_MODE: z.enum(["user", "organization"]).default("user"),
    GITHUB_ORGANIZATION: z.string().trim().default(""),
  })
  .superRefine((value, ctx) => {
    if (
      value.DEVICE_ACCESS_MODE === "organization" &&
      !/^[a-z\d](?:[a-z\d-]{0,37}[a-z\d])?$/i.test(value.GITHUB_ORGANIZATION)
    ) {
      ctx.addIssue({
        code: "custom",
        path: ["GITHUB_ORGANIZATION"],
        message:
          "GITHUB_ORGANIZATION must be a GitHub organization login in organization mode.",
      })
    }
  })

export function deviceAccessConfig(environment = process.env) {
  const config = accessConfigSchema.parse(environment)
  return {
    mode: config.DEVICE_ACCESS_MODE,
    organization: config.GITHUB_ORGANIZATION.toLowerCase(),
  }
}

export function deviceOwnerWhere(access: DeviceAccess) {
  if (!access.id) throw new Error("A device access identity is required.")
  return access.kind === "organization"
    ? { organizationId: access.id, userId: null }
    : { userId: access.id, organizationId: null }
}

export function deviceOwnerLabels(
  access: DeviceAccess,
): Record<string, string> {
  if (!access.id) throw new Error("A device access identity is required.")
  return access.kind === "organization"
    ? { organization: access.id }
    : { user: access.id }
}

export function deviceOwner(device: {
  userId: string | null
  organizationId: string | null
}): DeviceAccess {
  if (device.userId && !device.organizationId)
    return { kind: "user", id: device.userId }
  if (device.organizationId && !device.userId)
    return { kind: "organization", id: device.organizationId }
  throw new Error("A device must have exactly one owner.")
}
