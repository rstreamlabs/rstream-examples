import "server-only"

import { RstreamClient } from "@rstreamlabs/rstream"
import { type TunnelsProject } from "@rstreamlabs/rstream"
import { RstreamTunnelsClient } from "@rstreamlabs/tunnels"

import { type RstreamEnv } from "@/lib/env"
import { rstreamEnv } from "@/lib/env"

const DEFAULT_RSTREAM_API_URL = "https://rstream.io"

type ProjectCache = {
  key: string
  project: Promise<TunnelsProject>
}

declare global {
  var rstream: RstreamTunnelsClient | undefined
  var rstreamProject: ProjectCache | undefined
}

function rstreamCredentials(env: RstreamEnv) {
  return {
    clientId: env.RSTREAM_CLIENT_ID,
    clientSecret: env.RSTREAM_CLIENT_SECRET,
  }
}

function createRstreamClient() {
  const env = rstreamEnv()
  return new RstreamTunnelsClient({
    apiUrl: env.RSTREAM_API_URL ?? DEFAULT_RSTREAM_API_URL,
    credentials: rstreamCredentials(env),
    engine: env.RSTREAM_ENGINE,
    projectEndpoint: env.RSTREAM_PROJECT_ENDPOINT,
  })
}

function createControlPlaneClient(env: RstreamEnv) {
  return new RstreamClient({
    apiUrl: env.RSTREAM_API_URL ?? DEFAULT_RSTREAM_API_URL,
    credentials: rstreamCredentials(env),
  })
}

export async function getRstreamProjectId() {
  const env = rstreamEnv()
  if (env.RSTREAM_PROJECT_ID) {
    return env.RSTREAM_PROJECT_ID
  }
  const projectEndpoint = env.RSTREAM_PROJECT_ENDPOINT
  if (!projectEndpoint) {
    throw new Error(
      "RSTREAM_PROJECT_ID or RSTREAM_PROJECT_ENDPOINT is required.",
    )
  }
  const key = `${env.RSTREAM_API_URL ?? DEFAULT_RSTREAM_API_URL}:${env.RSTREAM_CLIENT_ID}:${projectEndpoint}`
  if (!globalThis.rstreamProject || globalThis.rstreamProject.key !== key) {
    const client = createControlPlaneClient(env)
    const project = client.tunnels.projects.resolveByEndpoint(projectEndpoint)
    globalThis.rstreamProject = {
      key,
      project: project.catch((err) => {
        if (globalThis.rstreamProject?.key === key) {
          globalThis.rstreamProject = undefined
        }
        throw err
      }),
    }
  }
  return (await globalThis.rstreamProject.project).id
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
