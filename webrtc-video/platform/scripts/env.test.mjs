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
  "MEDIAMTX_EXPOSURE",
  "MEDIAMTX_JWT_PRIVATE_KEY_BASE64",
  "MEDIAMTX_PUBLIC_URL",
  "MEDIAMTX_SOURCE_RESOLVER_JWKS",
  "MEDIAMTX_TUNNEL_NAME",
  "TURN_CREDENTIAL_TTL_SECONDS",
  "VIDEO_DISTRIBUTOR",
]

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
