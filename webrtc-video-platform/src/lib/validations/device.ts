import { z } from "zod"

export const deviceViewSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  secretPrefix: z.string().min(1),
  tunnelName: z.string().min(1),
  online: z.boolean(),
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

export const turnCredentialsSchema = z.object({
  urls: z.array(z.string().min(1)),
  username: z.string().min(1),
  credential: z.string().min(1),
})

export const viewerPayloadSchema = z.object({
  endpoints: z.object({
    ws: z.string().url(),
  }),
  turn: turnCredentialsSchema,
})

const sessionStatsSchema = z.object({
  codec: z.string(),
  twccEnabled: z.boolean(),
  nackEnabled: z.boolean(),
  rtxEnabled: z.boolean(),
  flexFECEnabled: z.boolean(),
  adaptiveBackend: z.enum(["off", "twcc-gcc"]),
  adaptiveActive: z.boolean(),
  estimatedBitrateBps: z.number().int(),
  encoderTargetBitrateKbps: z.number().int(),
  lastAppliedBitrateKbps: z.number().int(),
})

export const signalMessageSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("session.ready"),
    viewerId: z.string().optional(),
  }),
  z.object({
    type: z.literal("webrtc.answer"),
    sdp: z.string(),
  }),
  z.object({
    type: z.literal("webrtc.candidate"),
    candidate: z.string().optional(),
    sdpMid: z.string().nullable().optional(),
    sdpMLineIndex: z.number().optional(),
  }),
  z.object({
    type: z.literal("error"),
    message: z.string().optional(),
  }),
  z.object({
    type: z.literal("log"),
    message: z.string().optional(),
  }),
  z.object({
    type: z.literal("session.stats"),
    stats: sessionStatsSchema.optional(),
  }),
  z.object({
    type: z.literal("pong"),
  }),
])

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
export type TurnCredentials = z.infer<typeof turnCredentialsSchema>
export type SignalMessage = z.infer<typeof signalMessageSchema>
export type WatchPayload = z.infer<typeof watchPayloadSchema>
