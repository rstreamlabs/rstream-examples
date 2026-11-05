// MediaMTX muxes bounded clips on demand without HTTP byte-range support.
// Load one bounded clip before handing it to the media element: a Blob URL
// lets Safari seek within those bytes without another authenticated request.
export async function readRecordingMedia(
  response: Response,
  signal: AbortSignal,
): Promise<Blob> {
  const maximumBytes = 64 * 1024 * 1024
  const declared = response.headers.get("content-length")
  if (
    response.status !== 200 ||
    response.headers.get("content-type")?.split(";", 1)[0].trim() !==
      "video/mp4" ||
    (declared !== null &&
      (!/^\d+$/.test(declared) || Number(declared) > maximumBytes)) ||
    !response.body
  ) {
    await response.body?.cancel().catch(() => {})
    throw new Error("Recording media is unavailable")
  }
  const reader = response.body.getReader()
  const abort = () => {
    void reader.cancel().catch(() => {})
  }
  signal.addEventListener("abort", abort, { once: true })
  const chunks: ArrayBuffer[] = []
  let size = 0
  try {
    while (true) {
      signal.throwIfAborted()
      const { value, done } = await reader.read()
      signal.throwIfAborted()
      if (done) break
      size += value.byteLength
      if (size > maximumBytes) throw new Error("Recording media is too large")
      chunks.push(value.slice().buffer)
    }
    if (!size || (declared !== null && size !== Number(declared)))
      throw new Error("Recording media is incomplete")
    return new Blob(chunks, { type: "video/mp4" })
  } finally {
    signal.removeEventListener("abort", abort)
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
