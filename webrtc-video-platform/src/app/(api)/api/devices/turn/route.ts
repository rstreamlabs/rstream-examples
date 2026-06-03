import { requireDevice } from "@/lib/devices"
import { turnPayload } from "@/lib/devices"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"

const POST = withError(async (request: NextRequest) => {
  const device = await requireDevice(request)
  const turn = await turnPayload(device.id)
  return Response.json(turn, { status: 200 })
})

export { POST }
