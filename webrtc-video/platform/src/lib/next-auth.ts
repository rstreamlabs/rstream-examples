import "server-only"

import { getServerSession } from "next-auth/next"
import { HTTPError } from "@/lib/error"
import { PrismaAdapter } from "@auth/prisma-adapter"
import { type NextAuthOptions } from "next-auth"
import { type NextRequest } from "next/server"
import GithubProvider from "next-auth/providers/github"

import { requiredEnv } from "@/lib/env"
import prisma from "@/lib/prisma"
import { deviceAccessConfig, type DeviceAccess } from "@/lib/device-access"
import {
  GitHubMembershipVerifier,
  MembershipUnavailable,
} from "@/lib/github-membership"

const membershipVerifier = new GitHubMembershipVerifier()
const accessConfig = deviceAccessConfig()

export const authOptions: NextAuthOptions = {
  adapter: PrismaAdapter(prisma),
  providers: [
    GithubProvider({
      clientId: requiredEnv("GITHUB_CLIENT_ID"),
      clientSecret: requiredEnv("GITHUB_CLIENT_SECRET"),
      authorization: {
        params: {
          prompt: "select_account",
          scope:
            accessConfig.mode === "organization"
              ? "read:user user:email read:org"
              : "read:user user:email",
        },
      },
      httpOptions: {
        timeout: 30000,
      },
    }),
  ],
  callbacks: {
    async signIn({ account }) {
      if (accessConfig.mode === "user") return true
      if (account?.provider !== "github" || !account.access_token) return false
      try {
        const membership = await membershipVerifier.verify(
          accessConfig.organization,
          account.providerAccountId,
          account.access_token,
        )
        if (!membership) return false
        // NextAuth v4 does not refresh an existing Account's OAuth token itself.
        await prisma.account.updateMany({
          where: {
            provider: "github",
            providerAccountId: account.providerAccountId,
          },
          data: { access_token: account.access_token, scope: account.scope },
        })
        return true
      } catch {
        // NextAuth must not log upstream responses, OAuth tokens or request URLs.
        console.warn(
          "GitHub organization verification unavailable during sign-in",
        )
        return false
      }
    },
    session({ session, user }) {
      if (session.user) {
        session.user.id = user.id
      }
      return session
    },
  },
}

export async function getServerUser(signal?: AbortSignal) {
  const user = (await getServerSession(authOptions))?.user
  if (!user?.id) return null
  let access: DeviceAccess = { kind: "user", id: user.id }
  if (accessConfig.mode === "organization") {
    const account = await prisma.account.findFirst({
      where: { userId: user.id, provider: "github" },
      select: { providerAccountId: true, access_token: true },
    })
    if (!account?.access_token)
      throw new HTTPError(
        403,
        "GitHub organization membership is required. Sign in again.",
      )
    try {
      const membership = await membershipVerifier.verify(
        accessConfig.organization,
        account.providerAccountId,
        account.access_token,
        signal,
      )
      if (!membership)
        throw new HTTPError(
          403,
          "Active membership in the configured GitHub organization is required.",
        )
      access = { kind: "organization", id: membership.organizationId }
    } catch (error) {
      if (error instanceof MembershipUnavailable)
        throw new HTTPError(503, error.message)
      throw error
    }
  }
  return { ...user, access }
}

export type ServerUser = NonNullable<Awaited<ReturnType<typeof getServerUser>>>

export async function requireUser() {
  const user = await getServerUser()
  if (!user?.id) {
    return null
  }
  return user
}

export function withUser<Args extends unknown[]>(
  handler: (
    request: NextRequest,
    user: ServerUser,
    ...args: Args
  ) => Promise<Response>,
): (request: NextRequest, ...args: Args) => Promise<Response> {
  return async (request: NextRequest, ...args: Args): Promise<Response> => {
    if (!["GET", "HEAD", "OPTIONS"].includes(request.method)) {
      const origin = request.headers.get("origin")
      if (
        request.headers.get("sec-fetch-site") === "cross-site" ||
        (origin && origin !== new URL(requiredEnv("NEXTAUTH_URL")).origin)
      ) {
        throw new HTTPError(403, "Cross-origin mutations are not allowed.")
      }
    }
    const user = await getServerUser(request.signal)
    if (!user?.id) {
      throw new HTTPError(401, "Unauthorized")
    }
    return handler(request, user, ...args)
  }
}
