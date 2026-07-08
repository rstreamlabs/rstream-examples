import "server-only"

import { getServerSession } from "next-auth/next"
import { HTTPError } from "@/lib/error"
import { PrismaAdapter } from "@auth/prisma-adapter"
import { type NextAuthOptions } from "next-auth"
import { type NextRequest } from "next/server"
import GithubProvider from "next-auth/providers/github"

import { requiredEnv } from "@/lib/env"
import prisma from "@/lib/prisma"

export const authOptions: NextAuthOptions = {
  adapter: PrismaAdapter(prisma),
  providers: [
    GithubProvider({
      clientId: requiredEnv("GITHUB_CLIENT_ID"),
      clientSecret: requiredEnv("GITHUB_CLIENT_SECRET"),
      authorization: {
        params: {
          prompt: "select_account",
        },
      },
      httpOptions: {
        timeout: 30000,
      },
    }),
  ],
  callbacks: {
    session({ session, user }) {
      if (session.user) {
        session.user.id = user.id
      }
      return session
    },
  },
}

export async function getServerUser() {
  return (await getServerSession(authOptions))?.user ?? null
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
    const user = await getServerUser()
    if (!user?.id) {
      throw new HTTPError(401, "Unauthorized")
    }
    return handler(request, user, ...args)
  }
}
