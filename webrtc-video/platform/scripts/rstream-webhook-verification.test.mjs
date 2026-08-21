import assert from "node:assert/strict"
import { Buffer } from "node:buffer"
import test from "node:test"

import { verifiedRstreamWebhookEvent } from "../src/lib/rstream-webhook-verification.ts"

test("webhook verification never exposes cryptographic failure details", async () => {
  await assert.rejects(
    verifiedRstreamWebhookEvent(
      Buffer.from('{"type":"tunnel.created"}'),
      "invalid-signature",
      "test-signing-secret",
    ),
    (error) => {
      assert.equal(error.message, "Invalid rstream webhook payload.")
      assert.equal(error.message.includes("signature"), false)
      return true
    },
  )
})
