import { type NextRequest } from "next/server"
import { withUser } from "@/lib/next-auth"
import { findSourceDevice } from "@/lib/devices"
import { HTTPError, withError } from "@/lib/error"
import { mediaMTXPlayback, mediaMTXPath } from "@/lib/video-distributor"

type RouteContext = { params: Promise<{ device: string }> }

export const GET = withError(
  withUser(async (request: NextRequest, user, context: RouteContext) => {
    const { device: id } = await context.params
    const device = await findSourceDevice(id, user.access, request.signal)
    if (!device) throw new HTTPError(404, "Device not found")
    const playback = mediaMTXPlayback()
    const headers = { "Cache-Control": "private, no-store" }
    if (!playback) return new Response(null, { status: 204, headers })
    if (request.nextUrl.search)
      throw new HTTPError(
        400,
        "Recording index does not accept query parameters",
      )
    try {
      const index = await playback.index(
        mediaMTXPath(device.id),
        request.signal,
      )
      return Response.json(index, { headers })
    } catch {
      if (request.signal.aborted) throw new HTTPError(499, "Request cancelled")
      throw new HTTPError(503, "Recent recordings are unavailable")
    }
  }),
)
