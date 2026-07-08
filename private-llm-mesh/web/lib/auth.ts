import "server-only";

import {
  getServerSession,
  type NextAuthOptions,
  type Profile,
} from "next-auth";
import GithubProvider from "next-auth/providers/github";
import { z } from "zod";

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

/** Enabled sign-in methods. */
export function availableAuth(): { github: boolean; disabled: boolean } {
  const env = authEnv();
  return {
    github: Boolean(env.GITHUB_CLIENT_ID && env.GITHUB_CLIENT_SECRET),
    disabled: env.AUTH_DISABLED,
  };
}

function providers(): NextAuthOptions["providers"] {
  const env = authEnv();
  if (!env.GITHUB_CLIENT_ID || !env.GITHUB_CLIENT_SECRET) return [];
  return [
    GithubProvider({
      clientId: env.GITHUB_CLIENT_ID,
      clientSecret: env.GITHUB_CLIENT_SECRET,
      authorization: { params: { prompt: "select_account" } },
    }),
  ];
}

function githubAllowed(profile: Profile | undefined): boolean {
  const env = authEnv();
  const open =
    env.ALLOWED_EMAILS.length === 0 &&
    env.ALLOWED_EMAIL_DOMAINS.length === 0 &&
    env.ALLOWED_GITHUB_LOGINS.length === 0;
  if (open) return true;
  const parsed = githubProfileSchema.safeParse(profile);
  const email = (parsed.success ? parsed.data.email : "")?.toLowerCase() ?? "";
  const login = (parsed.success ? parsed.data.login : "")?.toLowerCase() ?? "";
  const domain = email.includes("@") ? email.split("@")[1] : "";
  return (
    (email !== "" && env.ALLOWED_EMAILS.includes(email)) ||
    (domain !== "" && env.ALLOWED_EMAIL_DOMAINS.includes(domain)) ||
    (login !== "" && env.ALLOWED_GITHUB_LOGINS.includes(login))
  );
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
