import "server-only"

import { rstreamEnv } from "@/lib/env"
import {
  platformRstreamClientFactory,
  requestScopedClient,
} from "@/lib/bounded-rstream"

const createClient = platformRstreamClientFactory()

export const getRstreamClient = requestScopedClient(
  async (signal?: AbortSignal) => {
    const env = rstreamEnv()
    return createClient(
      {
        apiUrl: env.RSTREAM_API_URL ?? "https://rstream.io",
        credentials: {
          clientId: env.RSTREAM_CLIENT_ID,
          clientSecret: env.RSTREAM_CLIENT_SECRET,
        },
        engine: env.RSTREAM_ENGINE,
        projectId: env.RSTREAM_PROJECT_ID,
        projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
      },
      signal,
    )
  },
)
