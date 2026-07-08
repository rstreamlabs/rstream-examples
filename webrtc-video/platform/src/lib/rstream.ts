import "server-only"

import { rstreamEnv } from "@/lib/env"
import { RstreamTunnelsClient } from "@rstreamlabs/tunnels"
import { type RstreamEnv } from "@/lib/env"

const DEFAULT_RSTREAM_API_URL = "https://rstream.io"

declare global {
  var rstream: RstreamTunnelsClient | undefined
}

function rstreamCredentials(env: RstreamEnv) {
  return {
    clientId: env.RSTREAM_CLIENT_ID,
    clientSecret: env.RSTREAM_CLIENT_SECRET,
  }
}

function createRstreamClient() {
  const env = rstreamEnv()
  // The platform uses the Engine-scoped tunnels client for one project only.
  return new RstreamTunnelsClient({
    apiUrl: env.RSTREAM_API_URL ?? DEFAULT_RSTREAM_API_URL,
    credentials: rstreamCredentials(env),
    engine: env.RSTREAM_ENGINE,
    projectId: env.RSTREAM_PROJECT_ID,
    projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
  })
}

export function getRstreamClient() {
  if (process.env.NODE_ENV === "production") {
    return createRstreamClient()
  }
  if (!globalThis.rstream) {
    globalThis.rstream = createRstreamClient()
  }
  return globalThis.rstream
}
