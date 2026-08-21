import assert from "node:assert/strict"
import test from "node:test"

import { rstreamEnvResult } from "../src/lib/env.ts"

const baseEnvironment = {
  RSTREAM_CLIENT_ID: "client",
  RSTREAM_CLIENT_SECRET: "secret",
  RSTREAM_PROJECT_ENDPOINT: "project",
}

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

function withEnvironment(environment, operation) {
  const previous = new Map()
  const names = new Set([
    ...Object.keys(environment),
    "TURN_CREDENTIAL_TTL_SECONDS",
  ])
  for (const name of names) {
    previous.set(name, process.env[name])
  }
  for (const [name, value] of Object.entries(environment)) {
    process.env[name] = value
  }
  if (!("TURN_CREDENTIAL_TTL_SECONDS" in environment)) {
    delete process.env.TURN_CREDENTIAL_TTL_SECONDS
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
