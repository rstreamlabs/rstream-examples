import "server-only"

import { RstreamTunnelsClient } from "@rstreamlabs/tunnels"

import { rstreamEnv } from "@/lib/env"

const DEFAULT_RSTREAM_API_URL = "https://rstream.io"

declare global {
  var rstream: RstreamTunnelsClient | undefined
}

function createRstreamClient() {
  const env = rstreamEnv()
  return new RstreamTunnelsClient({
    apiUrl: env.RSTREAM_API_URL ?? DEFAULT_RSTREAM_API_URL,
    credentials: {
      clientId: env.RSTREAM_CLIENT_ID,
      clientSecret: env.RSTREAM_CLIENT_SECRET,
    },
    engine: env.RSTREAM_ENGINE,
    projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
  })
}

const rstream =
  process.env.NODE_ENV === "production"
    ? createRstreamClient()
    : (globalThis.rstream ?? createRstreamClient())

if (process.env.NODE_ENV !== "production") {
  globalThis.rstream = rstream
}

export default rstream
