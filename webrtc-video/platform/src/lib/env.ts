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
    RSTREAM_API_URL: optionalUrlSchema,
    RSTREAM_ENGINE: optionalStringSchema,
    RSTREAM_TURN_KEYRING_BASE_URL: optionalUrlSchema,
    DEVICE_TOKEN_TTL_SECONDS: secondsSchema("300"),
    TURN_CREDENTIAL_TTL_SECONDS: secondsSchema("600"),
    VIEWER_TOKEN_TTL_SECONDS: secondsSchema("120"),
    WATCH_TOKEN_TTL_SECONDS: secondsSchema("120"),
    VIDEO_DISTRIBUTOR: z.enum(["direct", "mediamtx"]).default("direct"),
    MEDIAMTX_EXPOSURE: z.enum(["public", "rstream"]).default("rstream"),
    MEDIAMTX_PUBLIC_URL: optionalUrlSchema,
    MEDIAMTX_TUNNEL_NAME: optionalStringSchema,
    MEDIAMTX_SOURCE_RESOLVER_JWKS: optionalStringSchema,
    MEDIAMTX_SOURCE_RESOLVER_ISSUER: z
      .string()
      .trim()
      .min(1)
      .max(512)
      .default("rstream-video-distributor"),
    MEDIAMTX_SOURCE_RESOLVER_AUDIENCE: z
      .string()
      .trim()
      .min(1)
      .max(512)
      .default("rstream-video-source-resolver"),
    MEDIAMTX_JWT_PRIVATE_KEY_BASE64: optionalStringSchema,
    MEDIAMTX_JWT_ADDITIONAL_JWKS: optionalStringSchema,
    MEDIAMTX_JWT_ISSUER: z
      .string()
      .trim()
      .min(1)
      .max(512)
      .default("rstream-webrtc-video-platform"),
    MEDIAMTX_JWT_AUDIENCE: z
      .string()
      .trim()
      .min(1)
      .max(512)
      .default("rstream-mediamtx"),
    MEDIAMTX_TOKEN_TTL_SECONDS: secondsSchema("300"),
  })
  .superRefine((env, ctx) => {
    if (!env.RSTREAM_PROJECT_ID && !env.RSTREAM_PROJECT_ENDPOINT) {
      ctx.addIssue({
        code: "custom",
        path: ["RSTREAM_PROJECT_ID_OR_ENDPOINT"],
        message: "RSTREAM_PROJECT_ID or RSTREAM_PROJECT_ENDPOINT is required.",
      })
    }
    if (
      env.TURN_CREDENTIAL_TTL_SECONDS < 90 ||
      env.TURN_CREDENTIAL_TTL_SECONDS > 3600
    ) {
      ctx.addIssue({
        code: "custom",
        path: ["TURN_CREDENTIAL_TTL_SECONDS"],
        message:
          "TURN_CREDENTIAL_TTL_SECONDS must be from 90 through 3600 seconds.",
      })
    }
    if (env.VIDEO_DISTRIBUTOR !== "mediamtx") {
      return
    }
    for (const [name, value] of [
      ["MEDIAMTX_SOURCE_RESOLVER_JWKS", env.MEDIAMTX_SOURCE_RESOLVER_JWKS],
      ["MEDIAMTX_JWT_PRIVATE_KEY_BASE64", env.MEDIAMTX_JWT_PRIVATE_KEY_BASE64],
    ] as const) {
      if (!value) {
        ctx.addIssue({
          code: "custom",
          path: [name],
          message: `${name} is required when VIDEO_DISTRIBUTOR=mediamtx.`,
        })
      }
    }
    if (env.MEDIAMTX_EXPOSURE === "public") {
      if (!env.MEDIAMTX_PUBLIC_URL) {
        ctx.addIssue({
          code: "custom",
          path: ["MEDIAMTX_PUBLIC_URL"],
          message:
            "MEDIAMTX_PUBLIC_URL is required when MEDIAMTX_EXPOSURE=public.",
        })
      } else {
        validateMediaMTXPublicURL(env.MEDIAMTX_PUBLIC_URL, ctx)
      }
      if (env.MEDIAMTX_TUNNEL_NAME) {
        ctx.addIssue({
          code: "custom",
          path: ["MEDIAMTX_TUNNEL_NAME"],
          message:
            "MEDIAMTX_TUNNEL_NAME must be empty when MEDIAMTX_EXPOSURE=public.",
        })
      }
    } else {
      if (!env.MEDIAMTX_TUNNEL_NAME) {
        ctx.addIssue({
          code: "custom",
          path: ["MEDIAMTX_TUNNEL_NAME"],
          message:
            "MEDIAMTX_TUNNEL_NAME is required when MEDIAMTX_EXPOSURE=rstream.",
        })
      }
      if (env.MEDIAMTX_PUBLIC_URL) {
        ctx.addIssue({
          code: "custom",
          path: ["MEDIAMTX_PUBLIC_URL"],
          message:
            "MEDIAMTX_PUBLIC_URL must be empty when MEDIAMTX_EXPOSURE=rstream.",
        })
      }
    }
    if (env.MEDIAMTX_TOKEN_TTL_SECONDS > 3600) {
      ctx.addIssue({
        code: "custom",
        path: ["MEDIAMTX_TOKEN_TTL_SECONDS"],
        message: "MEDIAMTX_TOKEN_TTL_SECONDS must not exceed 3600 seconds.",
      })
    }
  })

function validateMediaMTXPublicURL(rawURL: string, ctx: z.RefinementCtx) {
  const url = new URL(rawURL)
  const loopback = new Set(["127.0.0.1", "::1", "localhost"]).has(url.hostname)
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) {
    ctx.addIssue({
      code: "custom",
      path: ["MEDIAMTX_PUBLIC_URL"],
      message:
        "MEDIAMTX_PUBLIC_URL must use HTTPS, except for a loopback development URL.",
    })
  }
  if (url.username || url.password || url.search || url.hash) {
    ctx.addIssue({
      code: "custom",
      path: ["MEDIAMTX_PUBLIC_URL"],
      message:
        "MEDIAMTX_PUBLIC_URL must not contain credentials, a query, or a fragment.",
    })
  }
}

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
  return z
    .enum(["true", "false", "1", "0"])
    .default("false")
    .transform((value) => value === "true" || value === "1")
    .parse(process.env.DEMO_CLEANUP_ENABLED)
}

export function rstreamWebhookSigningSecret(): string | null {
  return process.env.RSTREAM_WEBHOOK_SIGNING_SECRET?.trim() || null
}

export function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required.`)
  }
  return value
}
