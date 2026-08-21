import assert from "node:assert/strict"
import test from "node:test"

import { issueSourceCredentials } from "../src/lib/video-source-resolution.ts"
import { viewerDistributionPreferenceSchema } from "../src/lib/validations/device.ts"

test("viewer distribution preference permits only automatic or direct selection", () => {
  assert.equal(viewerDistributionPreferenceSchema.parse(undefined), "automatic")
  assert.equal(
    viewerDistributionPreferenceSchema.parse("automatic"),
    "automatic",
  )
  assert.equal(viewerDistributionPreferenceSchema.parse("direct"), "direct")
  assert.throws(() => viewerDistributionPreferenceSchema.parse("mediamtx"))
})

test("session source resolution issues media and TURN credentials", async () => {
  const calls = []
  const credentials = await issueSourceCredentials("session", {
    issueSource: async () => {
      calls.push("source")
      return "source-credential"
    },
    issueDestination: async () => {
      calls.push("destination")
      return "destination-credential"
    },
    issueTURN: async () => {
      calls.push("turn")
      return "turn-credential"
    },
  })
  assert.deepEqual(calls.sort(), ["destination", "source", "turn"])
  assert.deepEqual(credentials, {
    source: "source-credential",
    destination: "destination-credential",
    turn: "turn-credential",
  })
})

test("signaling refresh never issues TURN credentials", async () => {
  let turnCalls = 0
  const credentials = await issueSourceCredentials("signaling", {
    issueSource: async () => "source-credential",
    issueDestination: async () => "destination-credential",
    issueTURN: async () => {
      turnCalls += 1
      return "turn-credential"
    },
  })
  assert.equal(turnCalls, 0)
  assert.deepEqual(credentials, {
    source: "source-credential",
    destination: "destination-credential",
    turn: null,
  })
})

test("failed signaling refresh does not touch TURN", async () => {
  let turnCalls = 0
  await assert.rejects(
    issueSourceCredentials("signaling", {
      issueSource: async () => {
        throw new Error("source credential failed")
      },
      issueDestination: async () => "destination-credential",
      issueTURN: async () => {
        turnCalls += 1
        return "turn-credential"
      },
    }),
    /source credential failed/,
  )
  assert.equal(turnCalls, 0)
})
