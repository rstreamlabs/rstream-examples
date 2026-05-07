import "server-only"

import { PrismaClient } from "@/prisma/generated/client"
import { PrismaPg } from "@prisma/adapter-pg"

declare global {
  var prisma: PrismaClient | undefined
}

function databaseUrl(): string {
  const value = process.env.POSTGRES_PRISMA_POOL_URL
  if (!value) {
    throw new Error("POSTGRES_PRISMA_POOL_URL is required")
  }
  return normalizeDatabaseUrl(value)
}

function normalizeDatabaseUrl(value: string): string {
  const url = new URL(value)
  const sslmode = url.searchParams.get("sslmode")
  if (
    sslmode === "prefer" ||
    sslmode === "require" ||
    sslmode === "verify-ca"
  ) {
    url.searchParams.set("sslmode", "verify-full")
  }
  return url.toString()
}

function createPrismaClient(): PrismaClient {
  return new PrismaClient({
    adapter: new PrismaPg({ connectionString: databaseUrl() }),
  })
}

const prisma =
  process.env.NODE_ENV === "production"
    ? createPrismaClient()
    : (globalThis.prisma ?? createPrismaClient())

if (process.env.NODE_ENV !== "production") {
  globalThis.prisma = prisma
}

export default prisma
