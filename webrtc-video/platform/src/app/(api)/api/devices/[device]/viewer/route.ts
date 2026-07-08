import { HTTPError } from "@/lib/error"
import { type NextRequest } from "next/server"
import { viewerPayload } from "@/lib/devices"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"
import prisma from "@/lib/prisma"

type RouteContext = {
  params: Promise<{ device: string }>
}

const POST = withError(
  withUser(async (_request: NextRequest, user, context: RouteContext) => {
    const { device: deviceId } = await context.params
    const device = await prisma.device.findFirst({
      where: {
        id: deviceId,
        userId: user.id,
      },
    })
    if (!device) {
      throw new HTTPError(404, "Device not found")
    }
    const payload = await viewerPayload(device)
    if (!payload) {
      throw new HTTPError(409, "Device is offline")
    }
    return Response.json(payload, { status: 200 })
  }),
)

export { POST }
