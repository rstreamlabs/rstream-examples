import assert from "node:assert/strict"
import test from "node:test"

import { viewerPayloadSchema } from "../src/lib/validations/device.ts"

const expiresAt = "2026-08-21T20:05:01.123456+02:00"
const turn = {
  credential: "secret",
  expiresAt,
  ttl: 600,
  urls: ["turn:relay.example:3478?transport=udp"],
  username: "viewer",
}

for (const distributor of [
  {
    authorization: "",
    expiresAt,
    kind: "direct",
    whep: "https://device.example/whep",
  },
  {
    authorization: "Bearer media-token",
    expiresAt,
    kind: "mediamtx",
    whep: "https://media.example/device/whep",
  },
]) {
  test(`${distributor.kind} viewer accepts RFC 3339 credential offsets`, () => {
    assert.equal(
      viewerPayloadSchema.safeParse({ distributor, turn }).success,
      true,
    )
  })
}
