import { z } from "zod"

function secondsSchema(defaultValue: string) {
  return z
    .string()
    .trim()
    .regex(/^[1-9]\d*$/)
    .default(defaultValue)
    .transform((value) => Number.parseInt(value, 10))
}

function booleanSchema(defaultValue: "true" | "false" | "1" | "0") {
  return z
    .enum(["true", "false", "1", "0"])
    .default(defaultValue)
    .transform((value) => value === "true" || value === "1")
}

const optionalStringSchema = z
  .string()
  .trim()
  .optional()
  .transform((value) => (value && value.length > 0 ? value : undefined))

const optionalUrlSchema = z
  .string()
  .trim()
  .url()
  .or(z.literal(""))
  .optional()
  .transform((value) => (value && value.length > 0 ? value : undefined))

const rstreamEnvSchema = z.object({
  RSTREAM_CLIENT_ID: z.string().trim().min(1),
  RSTREAM_CLIENT_SECRET: z.string().trim().min(1),
  RSTREAM_PROJECT_ENDPOINT: z.string().trim().min(1),
  RSTREAM_FINE_GRAINED_GRANTS: booleanSchema("true"),
  RSTREAM_API_URL: optionalUrlSchema,
  RSTREAM_ENGINE: optionalStringSchema,
  DEVICE_TOKEN_TTL_SECONDS: secondsSchema("300"),
  VIEWER_TOKEN_TTL_SECONDS: secondsSchema("120"),
})

export type RstreamEnv = z.infer<typeof rstreamEnvSchema>

export function rstreamEnv(): RstreamEnv {
  return rstreamEnvSchema.parse(process.env)
}

export function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required.`)
  }
  return value
}
