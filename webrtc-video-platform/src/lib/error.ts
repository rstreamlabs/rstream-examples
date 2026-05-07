import { type NextRequest } from "next/server"
import { ZodError } from "zod"

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

export async function readJSON(request: Request): Promise<unknown> {
  try {
    return await request.json()
  } catch {
    throw new HTTPError(400, "Invalid JSON body.")
  }
}

export function withError<Args extends unknown[]>(
  handler: (request: NextRequest, ...args: Args) => Promise<Response>,
): (request: NextRequest, ...args: Args) => Promise<Response> {
  return async (request: NextRequest, ...args: Args): Promise<Response> => {
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
