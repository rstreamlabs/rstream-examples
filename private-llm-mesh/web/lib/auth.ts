import "server-only";

import {
  getServerSession,
  type NextAuthOptions,
  type Profile,
} from "next-auth";
import GithubProvider from "next-auth/providers/github";
import { z } from "zod";

import {
  hasGithubAllowlist,
  isGithubIdentityAllowed,
  type GithubAllowlist,
} from "./auth-policy";
import { authEnv } from "./env";

export interface AuthedUser {
  id: string;
  name?: string | null;
  email?: string | null;
  image?: string | null;
}

const LOCAL_USER: AuthedUser = { id: "local", name: "Local user" };

const githubProfileSchema = z
  .object({ email: z.string().optional(), login: z.string().optional() })
  .partial();

function githubAllowlist(): GithubAllowlist {
  const env = authEnv();
  return {
    emails: env.ALLOWED_EMAILS,
    domains: env.ALLOWED_EMAIL_DOMAINS,
    logins: env.ALLOWED_GITHUB_LOGINS,
  };
}

/** Enabled sign-in methods. */
export function availableAuth(): { github: boolean; disabled: boolean } {
  const env = authEnv();
  return {
    github: Boolean(
      env.GITHUB_CLIENT_ID &&
      env.GITHUB_CLIENT_SECRET &&
      hasGithubAllowlist(githubAllowlist()),
    ),
    disabled: env.AUTH_DISABLED,
  };
}

function providers(): NextAuthOptions["providers"] {
  const env = authEnv();
  if (
    !env.GITHUB_CLIENT_ID ||
    !env.GITHUB_CLIENT_SECRET ||
    !hasGithubAllowlist(githubAllowlist())
  ) {
    return [];
  }
  return [
    GithubProvider({
      clientId: env.GITHUB_CLIENT_ID,
      clientSecret: env.GITHUB_CLIENT_SECRET,
      authorization: { params: { prompt: "select_account" } },
    }),
  ];
}

function githubAllowed(profile: Profile | undefined): boolean {
  const parsed = githubProfileSchema.safeParse(profile);
  if (!parsed.success) return false;
  return isGithubIdentityAllowed(parsed.data, githubAllowlist());
}

export const authOptions: NextAuthOptions = {
  session: { strategy: "jwt" },
  secret: authEnv().NEXTAUTH_SECRET,
  providers: providers(),
  callbacks: {
    signIn({ profile }) {
      return githubAllowed(profile);
    },
    session({ session, token }) {
      if (session.user && token.sub) session.user.id = token.sub;
      return session;
    },
  },
};

export async function getServerUser(): Promise<AuthedUser | null> {
  if (authEnv().AUTH_DISABLED) return LOCAL_USER;
  return (await getServerSession(authOptions))?.user ?? null;
}
