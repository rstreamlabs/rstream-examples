import "server-only";

import { RstreamTunnelsClient } from "@rstreamlabs/tunnels";

import { rstreamEnv } from "./env";

export const APP_LABEL = "private-llm-mesh";
export const ROLE_LABEL = "role";
export const LLM_ROLE = "llm";

declare global {
  var rstreamClient: RstreamTunnelsClient | undefined;
}

interface TokenCache {
  token: string;
  expiresAt: number;
}

interface RstreamCache {
  enginePromise: Promise<string> | null;
  probe: TokenCache | null;
}

const cache: RstreamCache = {
  enginePromise: null,
  probe: null,
};

function createClient(): RstreamTunnelsClient {
  const env = rstreamEnv();
  return new RstreamTunnelsClient({
    apiUrl: env.RSTREAM_API_URL,
    credentials: {
      clientId: env.RSTREAM_CLIENT_ID,
      clientSecret: env.RSTREAM_CLIENT_SECRET,
    },
    engine: env.RSTREAM_ENGINE,
    projectId: env.RSTREAM_PROJECT_ID,
    projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
  });
}

export function getRstreamClient(): RstreamTunnelsClient {
  if (process.env.NODE_ENV === "production") {
    return createClient();
  }
  const client = globalThis.rstreamClient ?? createClient();
  globalThis.rstreamClient = client;
  return client;
}

const llmPoolFilter = { labels: { [ROLE_LABEL]: LLM_ROLE, app: APP_LABEL } };

/** Resolved engine URL for the browser Watch connection. */
export function getEngine(): Promise<string> {
  cache.enginePromise ??= getRstreamClient().getEngine();
  return cache.enginePromise;
}

/**
 * Scoped token for probing workers: `/healthz` for live load on our own worker,
 * and `/v1/models` as the universal OpenAI liveness probe for any worker.
 */
export async function probeToken(): Promise<string> {
  const env = rstreamEnv();
  const now = Date.now();
  if (cache.probe && cache.probe.expiresAt - 30_000 > now) {
    return cache.probe.token;
  }
  const { token } = await getRstreamClient().auth.createAuthToken({
    expires_in: env.CONNECT_TOKEN_TTL_SECONDS,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            connect: {
              filters: { ...llmPoolFilter, status: "online" },
              params: { path: { regex: "^/(healthz|v1/models)$" } },
            },
          },
        },
      },
    },
  });
  cache.probe = {
    token,
    expiresAt: now + env.CONNECT_TOKEN_TTL_SECONDS * 1000,
  };
  return token;
}

/**
 * Scoped connect token for one worker tunnel and one request path. Its lifetime
 * (`CONNECT_TOKEN_TTL_SECONDS`, default 300s) must cover the whole turn, not one
 * request: an agent turn makes several calls to the worker, and on a slow worker
 * the later steps would otherwise start after the token expired.
 */
export async function mintConnectToken(
  tunnelId: string,
  pathRegex: string,
): Promise<string> {
  const { token } = await getRstreamClient().auth.createAuthToken({
    expires_in: rstreamEnv().CONNECT_TOKEN_TTL_SECONDS,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            connect: {
              filters: {
                id: tunnelId,
                status: "online",
                protocol: "http",
                publish: true,
              },
              params: { path: { regex: pathRegex } },
            },
          },
        },
      },
    },
  });
  return token;
}

/** Read-only token for browser tunnel-watch state. */
export async function watchToken(): Promise<string> {
  const env = rstreamEnv();
  const { token } = await getRstreamClient().auth.createAuthToken({
    expires_in: env.WATCH_TOKEN_TTL_SECONDS,
    permissions: ["tunnels.resources.read-only"],
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            list: {
              filters: llmPoolFilter,
              select: {
                id: true,
                client_id: true,
                name: true,
                status: true,
                protocol: true,
                publish: true,
                labels: true,
                host: true,
                hostname: true,
              },
            },
          },
        },
      },
    },
  });
  return token;
}

/** Hosted rstream control-plane MCP endpoint. */
export function mcpEndpoint(): string {
  const base = rstreamEnv().RSTREAM_API_URL ?? "https://rstream.io";
  return `${base.replace(/\/$/, "")}/api/mcp`;
}

/** Least-privilege token for the webtty MCP tools. */
export async function mintMcpToken(): Promise<string> {
  const { token } = await getRstreamClient().auth.createAuthToken({
    expires_in: 120,
    resources: {
      tunnels: {
        scopes: {
          tunnels: {
            list: true,
            connect: { filters: { status: "online" } },
          },
        },
      },
    },
  });
  return token;
}
