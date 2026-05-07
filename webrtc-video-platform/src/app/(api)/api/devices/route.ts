import { createDevice } from "@/lib/devices"
import { createDeviceParamsSchema } from "@/lib/validations/device"
import { deviceViews } from "@/lib/devices"
import { readJSON } from "@/lib/error"
import { toView } from "@/lib/devices"
import { type CreateDeviceResponse } from "@/lib/validations/device"
import { type ListDevicesResponse } from "@/lib/validations/device"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"

const GET = withError(
  withUser(async (_request: NextRequest, user) => {
    const res: ListDevicesResponse = {
      devices: await deviceViews(user.id),
    }
    return Response.json(res, { status: 200 })
  }),
)

const POST = withError(
  withUser(async (request: NextRequest, user) => {
    const params = createDeviceParamsSchema.parse(await readJSON(request))
    const created = await createDevice(user.id, params.name)
    const res: CreateDeviceResponse = {
      device: toView(created.device, false),
      secret: created.secret,
    }
    return Response.json(res, { status: 201 })
  }),
)

export { GET, POST }
