import { ZodError } from "zod"

const defaultMaximumJSONBytes = 64 * 1024

export class HTTPError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
  toJSON() {
    return {
      error: this.message,
    }
  }
}

function validationMessage(err: ZodError): string {
  return err.issues[0]?.message ?? "Invalid request payload."
}

function errorResponse(err: HTTPError): Response {
  return Response.json(err.toJSON(), { status: err.status })
}

export async function readJSON(
  request: Request,
  maximumBytes = defaultMaximumJSONBytes,
): Promise<unknown> {
  if (!Number.isSafeInteger(maximumBytes) || maximumBytes < 1) {
    throw new Error("JSON body limit must be a positive safe integer.")
  }
  const encoding = request.headers.get("content-encoding")?.trim().toLowerCase()
  if (encoding && encoding !== "identity") {
    throw new HTTPError(415, "Unsupported Content-Encoding.")
  }
  const contentLength = request.headers.get("content-length")?.trim()
  if (contentLength) {
    if (!/^\d+$/.test(contentLength)) {
      throw new HTTPError(400, "Invalid Content-Length.")
    }
    if (Number.parseInt(contentLength, 10) > maximumBytes) {
      throw new HTTPError(413, "Request body is too large.")
    }
  }
  if (!request.body) {
    throw new HTTPError(400, "Invalid JSON body.")
  }
  const reader = request.body.getReader()
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) {
        break
      }
      size += value.byteLength
      if (size > maximumBytes) {
        await reader.cancel().catch(() => undefined)
        throw new HTTPError(413, "Request body is too large.")
      }
      chunks.push(value)
    }
    const body = new Uint8Array(size)
    let offset = 0
    for (const chunk of chunks) {
      body.set(chunk, offset)
      offset += chunk.byteLength
    }
    const text = new TextDecoder("utf-8", { fatal: true }).decode(body)
    return JSON.parse(text)
  } catch (error) {
    if (error instanceof HTTPError || request.signal.aborted) {
      throw error
    }
    throw new HTTPError(400, "Invalid JSON body.")
  } finally {
    reader.releaseLock()
  }
}

export function withError<RequestType extends Request, Args extends unknown[]>(
  handler: (request: RequestType, ...args: Args) => Promise<Response>,
): (request: RequestType, ...args: Args) => Promise<Response> {
  return async (request: RequestType, ...args: Args): Promise<Response> => {
    try {
      return await handler(request, ...args)
    } catch (err) {
      if (err instanceof HTTPError) {
        return errorResponse(err)
      }
      if (err instanceof ZodError) {
        return errorResponse(new HTTPError(400, validationMessage(err)))
      }
      throw err
    }
  }
}
