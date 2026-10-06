import { appendFileSync } from "node:fs"
import { randomUUID } from "node:crypto"

// Qualification-only observation of the real server's admission lifecycle.
// MediaMTXPlayback aborts its private fetch signal when it releases the slot,
// whether by EOF, downstream cancellation, failure or deadline. Do not read,
// tee or buffer the response body, or log request headers/credentials.
const trace = process.env.RSTREAM_QUALIFICATION_RECORDING_TRACE
if (trace) {
  const original = globalThis.fetch
  globalThis.fetch = (input, init) => {
    let url
    try {
      url = new URL(input instanceof Request ? input.url : String(input))
    } catch {
      return original(input, init)
    }
    if (url.origin === "http://127.0.0.1:9996" && url.pathname === "/get") {
      const id = randomUUID()
      const write = (phase) =>
        appendFileSync(
          trace,
          JSON.stringify({
            id,
            phase,
            at: Date.now(),
            path: url.searchParams.get("path"),
            start: url.searchParams.get("start"),
            duration: Number(url.searchParams.get("duration")),
          }) + "\n",
          { mode: 0o600 },
        )
      write("opened")
      const signal =
        init?.signal ?? (input instanceof Request ? input.signal : null)
      if (!signal) throw new Error("Recording fetch has no cancellation signal")
      if (signal.aborted) write("released")
      else
        signal.addEventListener("abort", () => write("released"), {
          once: true,
        })
    }
    return original(input, init)
  }
}
