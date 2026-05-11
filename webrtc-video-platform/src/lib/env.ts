import { type ZodError } from "zod"
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

const rstreamEnvSchema = z
  .object({
    RSTREAM_CLIENT_ID: z.string().trim().min(1),
    RSTREAM_CLIENT_SECRET: z.string().trim().min(1),
    RSTREAM_PROJECT_ID: optionalStringSchema,
    RSTREAM_PROJECT_ENDPOINT: optionalStringSchema,
    RSTREAM_FINE_GRAINED_GRANTS: booleanSchema("true"),
    RSTREAM_API_URL: optionalUrlSchema,
    RSTREAM_ENGINE: optionalStringSchema,
    DEVICE_TOKEN_TTL_SECONDS: secondsSchema("300"),
    VIEWER_TOKEN_TTL_SECONDS: secondsSchema("120"),
  })
  .superRefine((env, ctx) => {
    if (!env.RSTREAM_PROJECT_ID && !env.RSTREAM_PROJECT_ENDPOINT) {
      ctx.addIssue({
        code: "custom",
        path: ["RSTREAM_PROJECT_ID_OR_ENDPOINT"],
        message: "RSTREAM_PROJECT_ID or RSTREAM_PROJECT_ENDPOINT is required.",
      })
    }
  })

export type RstreamEnv = z.infer<typeof rstreamEnvSchema>

export function rstreamEnvResult() {
  return rstreamEnvSchema.safeParse(process.env)
}

export function rstreamEnv(): RstreamEnv {
  const result = rstreamEnvResult()
  if (!result.success) {
    throw result.error
  }
  return result.data
}

export function rstreamConfigMissingMessage(error: ZodError): string {
  const customIssue = error.issues.find((issue) => issue.code === "custom")
  if (customIssue?.message) {
    return `rstream is not configured: ${customIssue.message}`
  }
  const names = [
    ...new Set(
      error.issues
        .map((issue) => issue.path[0])
        .filter((name): name is string => typeof name === "string"),
    ),
  ]
  if (names.length === 0) {
    return "rstream is not configured."
  }
  return `rstream is not configured: ${names.join(", ")}.`
}

export function demoCleanupEnabled(): boolean {
  return booleanSchema("false").parse(process.env.DEMO_CLEANUP_ENABLED)
}

export function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required.`)
  }
  return value
}
