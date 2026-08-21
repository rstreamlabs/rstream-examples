import { HTTPError } from "@/lib/error"
import { mediaMTXDeviceID } from "@/lib/video-distributor"
import { mediaMTXResolverAuthorized } from "@/lib/video-distributor"
import { mediaMTXSourcePayload } from "@/lib/devices"
import { mediaMTXSourceRequestSchema } from "@/lib/validations/video-distributor"
import { type NextRequest } from "next/server"
import { readJSON } from "@/lib/error"
import { videoDistributorMode } from "@/lib/video-distributor"
import { withError } from "@/lib/error"
import prisma from "@/lib/prisma"

const POST = withError(async (request: NextRequest) => {
  if (videoDistributorMode() !== "mediamtx") {
    throw new HTTPError(404, "MediaMTX distribution is disabled.")
  }
  if (!request.headers.has("authorization")) {
    throw new HTTPError(401, "Unauthorized")
  }
  const { path, purpose } = mediaMTXSourceRequestSchema.parse(
    await readJSON(request, 2 * 1024),
  )
  if (!mediaMTXResolverAuthorized(request, path, purpose)) {
    throw new HTTPError(401, "Unauthorized")
  }
  let deviceID: string
  try {
    deviceID = mediaMTXDeviceID(path)
  } catch {
    throw new HTTPError(400, "Invalid media path.")
  }
  const device = await prisma.device.findUnique({ where: { id: deviceID } })
  if (!device) {
    throw new HTTPError(404, "Device not found.")
  }
  const payload = await mediaMTXSourcePayload(device, purpose)
  if (!payload) {
    throw new HTTPError(409, "Device is offline.")
  }
  return Response.json(payload, {
    headers: { "Cache-Control": "no-store" },
    status: 200,
  })
})

export { POST }
