import assert from "node:assert/strict"
import test from "node:test"

import { rstreamEnvResult } from "../src/lib/env.ts"

const baseEnvironment = {
  RSTREAM_CLIENT_ID: "client",
  RSTREAM_CLIENT_SECRET: "secret",
  RSTREAM_PROJECT_ENDPOINT: "project",
}
const mediaMTXEnvironment = {
  ...baseEnvironment,
  MEDIAMTX_JWT_PRIVATE_KEY_BASE64: "private-key",
  MEDIAMTX_SOURCE_RESOLVER_JWKS: '{"keys":[]}',
  VIDEO_DISTRIBUTOR: "mediamtx",
}
const managedNames = [
  "RSTREAM_PROJECT_ID",
  "RSTREAM_PROJECT_ENDPOINT",
  "RSTREAM_ENGINE",
  "MEDIAMTX_EXPOSURE",
  "MEDIAMTX_METRICS_URL",
  "MEDIAMTX_PLAYBACK_URL",
  "MEDIAMTX_RECORDING_WINDOW_SECONDS",
  "MEDIAMTX_JWT_PRIVATE_KEY_BASE64",
  "MEDIAMTX_PUBLIC_URL",
  "MEDIAMTX_SOURCE_RESOLVER_JWKS",
  "MEDIAMTX_TUNNEL_NAME",
  "TURN_CREDENTIAL_TTL_SECONDS",
  "VIDEO_DISTRIBUTOR",
]

test("project ID and engine overrides cannot replace the endpoint required by TURN", () => {
  for (const endpoint of ["", "   "]) {
    withEnvironment(
      {
        ...baseEnvironment,
        RSTREAM_PROJECT_ENDPOINT: endpoint,
        RSTREAM_PROJECT_ID: "project-id",
        RSTREAM_ENGINE: "engine.test:443",
      },
      () => {
        const result = rstreamEnvResult()
        assert.equal(result.success, false)
        assert.deepEqual(
          result.error.issues.map((issue) => issue.path),
          [["RSTREAM_PROJECT_ENDPOINT"]],
        )
      },
    )
  }
  withEnvironment(
    { ...baseEnvironment, RSTREAM_PROJECT_ID: "project-id" },
    () => {
      assert.equal(rstreamEnvResult().success, true)
    },
  )
})

test("TURN credential TTL defaults to ten minutes", () => {
  withEnvironment(baseEnvironment, () => {
    const result = rstreamEnvResult()
    assert.equal(result.success, true)
    assert.equal(result.data.TURN_CREDENTIAL_TTL_SECONDS, 600)
  })
})

test("TURN credential TTL remains bounded", () => {
  for (const value of ["89", "3601"]) {
    withEnvironment(
      { ...baseEnvironment, TURN_CREDENTIAL_TTL_SECONDS: value },
      () => {
        const result = rstreamEnvResult()
        assert.equal(result.success, false)
        assert.match(result.error.message, /TURN_CREDENTIAL_TTL_SECONDS/)
      },
    )
  }
})

test("MediaMTX supports a public endpoint without an rstream tunnel", () => {
  withEnvironment(
    {
      ...mediaMTXEnvironment,
      MEDIAMTX_EXPOSURE: "public",
      MEDIAMTX_PUBLIC_URL: "https://media.example/base",
    },
    () => {
      const result = rstreamEnvResult()
      assert.equal(result.success, true)
      assert.equal(result.data.MEDIAMTX_EXPOSURE, "public")
      assert.equal(result.data.MEDIAMTX_TUNNEL_NAME, undefined)
    },
  )
})

test("MediaMTX supports an rstream endpoint without a public URL", () => {
  withEnvironment(
    {
      ...mediaMTXEnvironment,
      MEDIAMTX_EXPOSURE: "rstream",
      MEDIAMTX_TUNNEL_NAME: "mediamtx-eu-1",
    },
    () => {
      const result = rstreamEnvResult()
      assert.equal(result.success, true)
      assert.equal(result.data.MEDIAMTX_EXPOSURE, "rstream")
      assert.equal(result.data.MEDIAMTX_PUBLIC_URL, undefined)
    },
  )
})

test("MediaMTX endpoint modes reject missing and conflicting settings", () => {
  for (const environment of [
    { ...mediaMTXEnvironment, MEDIAMTX_EXPOSURE: "public" },
    {
      ...mediaMTXEnvironment,
      MEDIAMTX_EXPOSURE: "public",
      MEDIAMTX_PUBLIC_URL: "https://media.example",
      MEDIAMTX_TUNNEL_NAME: "unused",
    },
    { ...mediaMTXEnvironment, MEDIAMTX_EXPOSURE: "rstream" },
    {
      ...mediaMTXEnvironment,
      MEDIAMTX_EXPOSURE: "rstream",
      MEDIAMTX_PUBLIC_URL: "https://unused.example",
      MEDIAMTX_TUNNEL_NAME: "mediamtx-eu-1",
    },
  ]) {
    withEnvironment(environment, () => {
      assert.equal(rstreamEnvResult().success, false)
    })
  }
})

test("public MediaMTX endpoints require protected transport", () => {
  for (const publicURL of [
    "http://media.example",
    "https://user:secret@media.example",
    "https://media.example?token=secret",
    "https://media.example#fragment",
  ]) {
    withEnvironment(
      {
        ...mediaMTXEnvironment,
        MEDIAMTX_EXPOSURE: "public",
        MEDIAMTX_PUBLIC_URL: publicURL,
      },
      () => {
        const result = rstreamEnvResult()
        assert.equal(result.success, false)
        assert.match(result.error.message, /MEDIAMTX_PUBLIC_URL/)
      },
    )
  }
  withEnvironment(
    {
      ...mediaMTXEnvironment,
      MEDIAMTX_EXPOSURE: "public",
      MEDIAMTX_PUBLIC_URL: "http://localhost:8889",
    },
    () => assert.equal(rstreamEnvResult().success, true),
  )
})

test("MediaMTX metrics are optional, server-only and reject ambiguous configuration", () => {
  const base = {
    ...mediaMTXEnvironment,
    MEDIAMTX_EXPOSURE: "public",
    MEDIAMTX_PUBLIC_URL: "https://media.example",
  }
  for (const value of [
    "",
    "http://127.0.0.1:9998/metrics",
    "http://mediamtx.internal:9998/metrics",
    "https://private.example/prefix/metrics",
  ]) {
    withEnvironment({ ...base, MEDIAMTX_METRICS_URL: value }, () =>
      assert.equal(rstreamEnvResult().success, true),
    )
  }
  for (const value of [
    "ftp://private/metrics",
    "https://user:secret@private/metrics",
    "http://private/metrics?path=other",
    "http://private/metrics#fragment",
    "http://private/whep",
  ]) {
    withEnvironment({ ...base, MEDIAMTX_METRICS_URL: value }, () =>
      assert.equal(rstreamEnvResult().success, false),
    )
  }
  withEnvironment(
    { ...baseEnvironment, MEDIAMTX_METRICS_URL: "http://private/metrics" },
    () => assert.equal(rstreamEnvResult().success, false),
  )
})

function withEnvironment(environment, operation) {
  const previous = new Map()
  const names = new Set([...managedNames, ...Object.keys(environment)])
  for (const name of names) {
    previous.set(name, process.env[name])
  }
  for (const [name, value] of Object.entries(environment)) {
    process.env[name] = value
  }
  for (const name of managedNames) {
    if (!(name in environment)) {
      delete process.env[name]
    }
  }
  try {
    operation()
  } finally {
    for (const [name, value] of previous) {
      if (value === undefined) {
        delete process.env[name]
      } else {
        process.env[name] = value
      }
    }
  }
}

test("recording service is opt-in and rejects incompatible modes, URLs and windows", () => {
  withEnvironment(baseEnvironment, () =>
    assert.equal(rstreamEnvResult().data.MEDIAMTX_PLAYBACK_URL, undefined),
  )
  const recording = {
    ...mediaMTXEnvironment,
    MEDIAMTX_EXPOSURE: "public",
    MEDIAMTX_PUBLIC_URL: "https://media.example",
    MEDIAMTX_PLAYBACK_URL: "http://127.0.0.1:9996",
  }
  withEnvironment(recording, () => {
    assert.equal(rstreamEnvResult().success, true)
    assert.equal(rstreamEnvResult().data.MEDIAMTX_RECORDING_WINDOW_SECONDS, 300)
  })
  for (const settings of [
    { VIDEO_DISTRIBUTOR: "direct" },
    { MEDIAMTX_PLAYBACK_URL: "file:///tmp" },
    { MEDIAMTX_PLAYBACK_URL: "http://u:p@host" },
    { MEDIAMTX_PLAYBACK_URL: "http://host/?path=x" },
    { MEDIAMTX_RECORDING_WINDOW_SECONDS: "29" },
    { MEDIAMTX_RECORDING_WINDOW_SECONDS: "601" },
  ])
    withEnvironment({ ...recording, ...settings }, () =>
      assert.equal(rstreamEnvResult().success, false),
    )
})
