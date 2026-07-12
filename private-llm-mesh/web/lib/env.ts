import "server-only";

import { z } from "zod";

const optionalString = z
  .string()
  .trim()
  .optional()
  .transform((value) => (value && value.length > 0 ? value : undefined));

const seconds = (fallback: string) =>
  z
    .string()
    .trim()
    .regex(/^[1-9]\d*$/)
    .default(fallback)
    .transform((value) => Number.parseInt(value, 10));

const rstreamEnvSchema = z
  .object({
    RSTREAM_CLIENT_ID: z.string().trim().min(1),
    RSTREAM_CLIENT_SECRET: z.string().trim().min(1),
    RSTREAM_PROJECT_ID: optionalString,
    RSTREAM_PROJECT_ENDPOINT: optionalString,
    RSTREAM_API_URL: optionalString,
    RSTREAM_ENGINE: optionalString,
    CONNECT_TOKEN_TTL_SECONDS: seconds("300"),
    WATCH_TOKEN_TTL_SECONDS: seconds("120"),
  })
  .superRefine((env, ctx) => {
    if (!env.RSTREAM_PROJECT_ID && !env.RSTREAM_PROJECT_ENDPOINT) {
      ctx.addIssue({
        code: "custom",
        message:
          "Set RSTREAM_PROJECT_ID or RSTREAM_PROJECT_ENDPOINT to scope worker tokens.",
      });
    }
  });

export type RstreamEnv = z.infer<typeof rstreamEnvSchema>;

export function rstreamEnv(): RstreamEnv {
  return rstreamEnvSchema.parse(process.env);
}

const csvLower = z
  .string()
  .trim()
  .optional()
  .transform((value) =>
    value
      ? value
          .split(",")
          .map((entry) => entry.trim().toLowerCase())
          .filter(Boolean)
      : [],
  );

const boolean = z
  .string()
  .trim()
  .optional()
  .transform((value) => value === "true" || value === "1");

const authEnvSchema = z.object({
  NEXTAUTH_SECRET: optionalString,
  GITHUB_CLIENT_ID: optionalString,
  GITHUB_CLIENT_SECRET: optionalString,
  AUTH_DISABLED: boolean,
  ALLOWED_EMAILS: csvLower,
  ALLOWED_EMAIL_DOMAINS: csvLower,
  ALLOWED_GITHUB_LOGINS: csvLower,
});

export type AuthEnv = z.infer<typeof authEnvSchema>;

export function authEnv(): AuthEnv {
  return authEnvSchema.parse(process.env);
}
