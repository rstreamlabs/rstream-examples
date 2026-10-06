import { type NextRequest } from "next/server"
import { z } from "zod"
import { withUser } from "@/lib/next-auth"
import { findSourceDevice } from "@/lib/devices"
import { qualityEndpoint } from "@/lib/devices"
import { HTTPError, readJSON, withError } from "@/lib/error"
import { parseQualityState } from "@/lib/quality-client"

const selectionSchema = z
  .object({
    mode: z.string().regex(/^[a-z][a-z0-9-]{0,31}$/),
    version: z.string().regex(/^[a-f0-9]{32}:[0-9]{1,20}$/),
  })
  .strict()
type RouteContext = { params: Promise<{ device: string }> }

const handle = withError(
  withUser(async (request: NextRequest, user, context: RouteContext) => {
    const { device: id } = await context.params
    const device = await findSourceDevice(id, user.access, request.signal)
    if (!device) throw new HTTPError(404, "Device not found")
    const selection =
      request.method === "PUT"
        ? selectionSchema.parse(await readJSON(request, 2048))
        : null
    const signal = AbortSignal.any([request.signal, AbortSignal.timeout(7000)])
    try {
      const url = await qualityEndpoint(device, signal)
      const response = await fetch(url, {
        method: request.method,
        redirect: "error",
        cache: "no-store",
        headers: selection ? { "Content-Type": "application/json" } : {},
        body: selection ? JSON.stringify(selection) : undefined,
        signal,
      })
      if (
        request.method === "GET" &&
        (response.status === 204 || response.status === 404)
      ) {
        await response.body?.cancel()
        return new Response(null, {
          status: 204,
          headers: { "Cache-Control": "no-store" },
        })
      }
      if (!response.ok) {
        await response.body?.cancel()
        if (response.status === 404)
          throw new HTTPError(404, "Quality presets are not configured")
        if (response.status === 409)
          throw new HTTPError(
            409,
            "Source quality changed. Refresh and select again.",
          )
        if (response.status === 400)
          throw new HTTPError(400, "Unsupported source quality mode")
        throw new HTTPError(503, "Device control is unavailable")
      }
      // Reuse the streaming body bound; no upstream URL/body is logged or returned.
      let state
      try {
        state = parseQualityState(await readJSON(response, 16 * 1024))
      } catch {
        throw new HTTPError(503, "Invalid device control response")
      }
      if (selection)
        console.info("Source quality selected", {
          deviceId: device.id,
          userId: user.id,
          mode: selection.mode,
        })
      return Response.json(state, { headers: { "Cache-Control": "no-store" } })
    } catch (error) {
      if (error instanceof HTTPError) throw error
      if (request.signal.aborted) throw new HTTPError(499, "Request cancelled")
      throw new HTTPError(503, "Device control is unavailable")
    }
  }),
)

export { handle as GET, handle as PUT }
