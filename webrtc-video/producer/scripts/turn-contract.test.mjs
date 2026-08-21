import assert from "node:assert/strict";
import test from "node:test";

import { expiringTurnCredentialsSchema } from "../internal/web/embed/contracts.ts";

const credentials = {
  credential: "secret",
  ttl: 600,
  urls: ["turn:relay.example:3478?transport=udp"],
  username: "viewer",
};

test("the viewer accepts RFC 3339 deadlines with an explicit offset", () => {
  const result = expiringTurnCredentialsSchema.safeParse({
    ...credentials,
    expiresAt: "2026-08-21T20:05:01.123456+02:00",
  });
  assert.equal(result.success, true);
});

test("the viewer rejects an invalid credential deadline", () => {
  const result = expiringTurnCredentialsSchema.safeParse({
    ...credentials,
    expiresAt: "soon",
  });
  assert.equal(result.success, false);
});
