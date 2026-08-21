import { HTTPError } from "@/lib/error"
import { type NextRequest } from "next/server"
import { viewerPayload } from "@/lib/devices"
import { viewerDistributionPreferenceSchema } from "@/lib/validations/device"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"
import prisma from "@/lib/prisma"

type RouteContext = {
  params: Promise<{ device: string }>
}

const POST = withError(
  withUser(async (request: NextRequest, user, context: RouteContext) => {
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
    const distribution = viewerDistributionPreferenceSchema.parse(
      request.nextUrl.searchParams.get("distribution") ?? undefined,
    )
    const payload = await viewerPayload(device, distribution)
    if (!payload) {
      throw new HTTPError(409, "Device is offline")
    }
    return Response.json(payload, {
      headers: { "Cache-Control": "no-store" },
      status: 200,
    })
  }),
)

export { POST }
