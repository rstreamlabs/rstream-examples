import { HTTPError } from "@/lib/error"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"
import prisma from "@/lib/prisma"

type RouteContext = {
  params: Promise<{ device: string }>
}

const DELETE = withError(
  withUser(async (_request: NextRequest, user, context: RouteContext) => {
    const { device } = await context.params
    const deleted = await prisma.device.deleteMany({
      where: {
        id: device,
        userId: user.id,
      },
    })
    if (deleted.count === 0) {
      throw new HTTPError(404, "Device not found")
    }
    return Response.json({ ok: true }, { status: 200 })
  }),
)

export { DELETE }
