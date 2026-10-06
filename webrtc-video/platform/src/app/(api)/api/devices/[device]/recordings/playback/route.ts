import { type NextRequest } from "next/server"
import { withUser } from "@/lib/next-auth"
import { findSourceDevice } from "@/lib/devices"
import { HTTPError, withError } from "@/lib/error"
import { RecordingNotFound } from "@/lib/mediamtx-playback"
import { mediaMTXPlayback, mediaMTXPath } from "@/lib/video-distributor"

type RouteContext = { params: Promise<{ device: string }> }

export const GET = withError(
  withUser(async (request: NextRequest, user, context: RouteContext) => {
    const { device: id } = await context.params
    const device = await findSourceDevice(id, user.access, request.signal)
    if (!device) throw new HTTPError(404, "Device not found")
    const playback = mediaMTXPlayback()
    if (!playback) throw new HTTPError(404, "Recent recordings are disabled")
    try {
      // Range requests receive the same bounded whole clip with status 200.
      // MediaMTX 1.20's on-demand MP4 muxer does not support byte ranges.
      return await playback.clip(
        mediaMTXPath(device.id),
        request.nextUrl.searchParams,
        request.signal,
      )
    } catch (error) {
      if (request.signal.aborted) throw new HTTPError(499, "Request cancelled")
      if (error instanceof RangeError) throw new HTTPError(400, error.message)
      if (error instanceof RecordingNotFound)
        throw new HTTPError(404, error.message)
      throw new HTTPError(503, "Recent recordings are unavailable")
    }
  }),
)
