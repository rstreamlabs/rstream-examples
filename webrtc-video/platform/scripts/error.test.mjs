import assert from "node:assert/strict"
import test from "node:test"

import { HTTPError, readJSON } from "../src/lib/error.ts"

test("JSON reader accepts a bounded streaming body", async () => {
  const request = streamingRequest([
    new TextEncoder().encode('{"path":"devices/'),
    new TextEncoder().encode('one","purpose":"session"}'),
  ])
  assert.deepEqual(await readJSON(request, 128), {
    path: "devices/one",
    purpose: "session",
  })
})

test("JSON reader rejects a declared oversized body before reading it", async () => {
  let bodyRead = false
  const request = {
    get body() {
      bodyRead = true
      throw new Error("body must not be read")
    },
    headers: new Headers({ "Content-Length": "129" }),
  }
  await assert.rejects(readJSON(request, 128), httpStatus(413))
  assert.equal(bodyRead, false)
})

test("JSON reader cancels a chunked body as soon as it crosses the limit", async () => {
  let cancelled = false
  const request = new Request("https://platform.example/source", {
    body: new ReadableStream({
      cancel() {
        cancelled = true
      },
      start(controller) {
        controller.enqueue(new Uint8Array(80))
        controller.enqueue(new Uint8Array(80))
      },
    }),
    duplex: "half",
    method: "POST",
  })
  await assert.rejects(readJSON(request, 128), httpStatus(413))
  assert.equal(cancelled, true)
})

test("JSON reader rejects encoded, malformed, and invalid UTF-8 bodies", async () => {
  const encoded = new Request("https://platform.example/source", {
    body: "{}",
    headers: { "Content-Encoding": "gzip" },
    method: "POST",
  })
  await assert.rejects(readJSON(encoded), httpStatus(415))
  const invalidLength = new Request("https://platform.example/source", {
    body: "{}",
    headers: { "Content-Length": "2, 2" },
    method: "POST",
  })
  await assert.rejects(readJSON(invalidLength), httpStatus(400))
  await assert.rejects(
    readJSON(streamingRequest([new Uint8Array([0xff])])),
    httpStatus(400),
  )
  await assert.rejects(
    readJSON(streamingRequest([new TextEncoder().encode("{")])),
    httpStatus(400),
  )
})

test("JSON reader rejects an invalid programmer-supplied bound", async () => {
  const request = new Request("https://platform.example/source", {
    body: "{}",
    method: "POST",
  })
  await assert.rejects(
    readJSON(request, 0),
    /JSON body limit must be a positive safe integer/,
  )
})

function streamingRequest(chunks) {
  return new Request("https://platform.example/source", {
    body: new ReadableStream({
      start(controller) {
        for (const chunk of chunks) {
          controller.enqueue(chunk)
        }
        controller.close()
      },
    }),
    duplex: "half",
    method: "POST",
  })
}

function httpStatus(status) {
  return (error) => error instanceof HTTPError && error.status === status
}
