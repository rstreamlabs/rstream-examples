import { requireDevice } from "@/lib/devices"
import { touchDevice } from "@/lib/devices"
import { tunnelPayload } from "@/lib/devices"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"

const POST = withError(async (request: NextRequest) => {
  const device = await requireDevice(request)
  const payload = await tunnelPayload(device)
  await touchDevice(device.id)
  return Response.json(payload, { status: 200 })
})

export { POST }
