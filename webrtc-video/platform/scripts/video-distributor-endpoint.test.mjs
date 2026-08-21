import assert from "node:assert/strict"
import test from "node:test"

import { mediaMTXWHEPURL } from "../src/lib/video-distributor-endpoint.ts"

test("public MediaMTX endpoints need no rstream edge credential", () => {
  assert.equal(
    mediaMTXWHEPURL(
      "https://media.example/base/",
      "/devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11/",
    ),
    "https://media.example/base/devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11/whep",
  )
})

test("rstream MediaMTX endpoints keep the edge token out of the path", () => {
  const url = new URL(
    mediaMTXWHEPURL(
      "https://media.example",
      "devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11",
      "edge-secret",
    ),
  )
  assert.equal(
    url.pathname,
    "/devices/fd8c2b34-1da2-4c71-8f38-343af59c0a11/whep",
  )
  assert.equal(url.searchParams.get("rstream.token"), "edge-secret")
})
