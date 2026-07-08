import { loadEnvConfig } from "@next/env"
import { defineConfig } from "prisma/config"
import { env } from "prisma/config"

loadEnvConfig(process.cwd())

export default defineConfig({
  schema: "prisma/schema.prisma",
  datasource: {
    url: env("POSTGRES_PRISMA_DIRECT_URL"),
  },
})
