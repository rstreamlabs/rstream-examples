import { type NextRequest } from "next/server"
import { withUser } from "@/lib/next-auth"
import { findSourceDevice } from "@/lib/devices"
import { HTTPError, withError } from "@/lib/error"
import { mediaMTXMetrics } from "@/lib/video-distributor"

type RouteContext = { params: Promise<{ device: string }> }

export const GET = withError(
  withUser(async (request: NextRequest, user, context: RouteContext) => {
    const { device: id } = await context.params
    const device = await findSourceDevice(id, user.access, request.signal)
    if (!device) throw new HTTPError(404, "Device not found")
    try {
      const metrics = await mediaMTXMetrics(device.id, request.signal)
      const headers = { "Cache-Control": "no-store" }
      return metrics
        ? Response.json(metrics, { headers })
        : new Response(null, { status: 204, headers })
    } catch {
      if (request.signal.aborted) throw new HTTPError(499, "Request cancelled")
      // Never return the private metrics URL, raw labels, body or credentials.
      throw new HTTPError(503, "Distribution metrics are unavailable")
    }
  }),
)
