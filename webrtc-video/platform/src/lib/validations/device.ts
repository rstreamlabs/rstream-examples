import { turnCredentialsSchema } from "@rstreamlabs/rstream/turn"
import { z } from "zod"

export const deviceViewSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  secretPrefix: z.string().min(1),
  tunnelName: z.string().min(1),
  online: z.boolean(),
  onlineSince: z.string().nullable(),
  lastSeenAt: z.string().nullable(),
  createdAt: z.string(),
})

export const createDeviceParamsSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "Enter a device name.")
    .max(80, "Device name must be 80 characters or less."),
})

export const listDevicesResponseSchema = z.object({
  devices: z.array(deviceViewSchema),
})

export const createDeviceResponseSchema = z.object({
  device: deviceViewSchema,
  secret: z.string().min(1),
})

export const viewerPayloadSchema = z.object({
  distributor: z.discriminatedUnion("kind", [
    z.object({
      kind: z.literal("direct"),
      whep: z.string().url(),
      authorization: z.string().max(8192),
      expiresAt: z.string().datetime({ offset: true }),
    }),
    z.object({
      kind: z.literal("mediamtx"),
      whep: z.string().url(),
      authorization: z.string().min(1).max(8192),
      expiresAt: z.string().datetime({ offset: true }),
    }),
  ]),
  // Reuse the SDK TURN schema so the browser contract tracks rstream releases.
  turn: turnCredentialsSchema.extend({
    expiresAt: z.string().datetime({ offset: true }),
  }),
})

export const viewerDistributionPreferenceSchema = z
  .enum(["automatic", "direct"])
  .default("automatic")

export const apiErrorSchema = z.object({
  error: z.string().min(1),
})

export const watchPayloadSchema = z.object({
  auth: z.object({
    token: z.string().min(1),
  }),
  engine: z.string().min(1),
})

export type DeviceView = z.infer<typeof deviceViewSchema>
export type ListDevicesResponse = z.infer<typeof listDevicesResponseSchema>
export type CreateDeviceResponse = z.infer<typeof createDeviceResponseSchema>
export type ViewerPayload = z.infer<typeof viewerPayloadSchema>
export type ViewerDistributionPreference = z.infer<
  typeof viewerDistributionPreferenceSchema
>
export type TurnCredentials = z.infer<typeof turnCredentialsSchema>
export type WatchPayload = z.infer<typeof watchPayloadSchema>
