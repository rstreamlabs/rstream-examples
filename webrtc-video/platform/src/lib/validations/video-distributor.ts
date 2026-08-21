import { z } from "zod"

export const mediaMTXSourceRequestSchema = z
  .object({
    path: z.string().trim().min(1).max(128),
    purpose: z.enum(["session", "signaling"]),
  })
  .strict()

export type MediaMTXSourcePurpose = z.infer<
  typeof mediaMTXSourceRequestSchema
>["purpose"]
