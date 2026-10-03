import { createHash } from "node:crypto"
import { z } from "zod"

const membershipSchema = z.object({
  state: z.enum(["active", "pending"]),
  organization: z.object({
    id: z.number().int().positive().safe(),
    login: z.string(),
  }),
  user: z.object({ id: z.number().int().positive().safe() }),
})

export class MembershipUnavailable extends Error {
  constructor() {
    super(
      "GitHub organization membership could not be verified. Retry shortly.",
    )
  }
}

type Membership = { organizationId: string } | null
type Entry = { result: Membership; checkedAt: number; expiresAt: number }

// Bounded process-local cache. Every replica independently enforces the same
// maximum authorization age. No stale-on-error and no tokens in cache keys/logs.
export class GitHubMembershipVerifier {
  private readonly cache = new Map<string, Entry>()
  private readonly pending = new Map<string, Promise<Membership>>()
  private readonly fetcher: typeof fetch
  private readonly now: () => number

  constructor(options: { fetch?: typeof fetch; now?: () => number } = {}) {
    this.fetcher = options.fetch ?? fetch
    this.now = options.now ?? Date.now
  }

  async verify(
    organization: string,
    githubUserId: string,
    token: string,
    signal?: AbortSignal,
  ): Promise<Membership> {
    signal?.throwIfAborted()
    if (
      !/^[a-z\d](?:[a-z\d-]{0,37}[a-z\d])?$/i.test(organization) ||
      !/^[1-9]\d*$/.test(githubUserId) ||
      !token ||
      token.length > 8192
    )
      return null
    const login = organization.toLowerCase()
    const key = createHash("sha256")
      .update(JSON.stringify([login, githubUserId, token]))
      .digest("hex")
    const now = this.now()
    const entry = this.cache.get(key)
    if (entry && entry.checkedAt <= now && entry.expiresAt > now)
      return entry.result
    this.cache.delete(key)
    let request = this.pending.get(key)
    if (!request) {
      if (this.pending.size >= 64) throw new MembershipUnavailable()
      request = this.lookup(login, githubUserId, token)
        .then((result) => {
          // Age starts before the HTTP request, not when a slow response arrives.
          for (const [cachedKey, cached] of this.cache) {
            if (cached.expiresAt <= this.now() || cached.checkedAt > this.now())
              this.cache.delete(cachedKey)
          }
          while (this.cache.size >= 512)
            this.cache.delete(this.cache.keys().next().value!)
          this.cache.set(key, {
            result,
            checkedAt: now,
            expiresAt: now + (result ? 60_000 : 5_000),
          })
          return result
        })
        .finally(() => {
          this.pending.delete(key)
        })
      this.pending.set(key, request)
    }
    return waitForMembership(request, signal)
  }

  private async lookup(
    organization: string,
    githubUserId: string,
    token: string,
  ): Promise<Membership> {
    try {
      const response = await this.fetcher(
        `https://api.github.com/user/memberships/orgs/${organization}`,
        {
          headers: {
            Accept: "application/vnd.github+json",
            Authorization: `Bearer ${token}`,
            "X-GitHub-Api-Version": "2022-11-28",
          },
          redirect: "error",
          cache: "no-store",
          signal: AbortSignal.timeout(5_000),
        },
      )
      if ([401, 403, 404].includes(response.status)) {
        await response.body?.cancel()
        return null
      }
      if (!response.ok) {
        await response.body?.cancel()
        throw new MembershipUnavailable()
      }
      const reader = response.body?.getReader()
      if (!reader) throw new MembershipUnavailable()
      let size = 0
      const chunks: Uint8Array[] = []
      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          size += value.byteLength
          if (size > 64 * 1024) {
            await reader.cancel()
            throw new MembershipUnavailable()
          }
          chunks.push(value)
        }
      } finally {
        reader.releaseLock()
      }
      const body = Buffer.concat(chunks)
      const membership = membershipSchema.parse(
        JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body)),
      )
      if (
        membership.state !== "active" ||
        membership.organization.login.toLowerCase() !== organization ||
        String(membership.user.id) !== githubUserId
      )
        return null
      return { organizationId: String(membership.organization.id) }
    } catch {
      throw new MembershipUnavailable()
    }
  }
}

async function waitForMembership(
  request: Promise<Membership>,
  signal?: AbortSignal,
) {
  if (!signal) return request
  let onAbort: () => void = () => {}
  const cancelled = new Promise<never>((_, reject) => {
    onAbort = () => reject(signal.reason)
    signal.addEventListener("abort", onAbort, { once: true })
    if (signal.aborted) onAbort()
  })
  // One cancelled HTTP client cannot cancel another caller's shared check.
  try {
    return await Promise.race([request, cancelled])
  } finally {
    signal.removeEventListener("abort", onAbort)
  }
}
