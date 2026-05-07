import { HTTPError } from "@/lib/error"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"
import prisma from "@/lib/prisma"

const GET = withError(async (request: NextRequest) => {
  requireCronSecret(request)
  const [devices, sessions, accounts, verificationTokens, users] =
    await prisma.$transaction([
      prisma.device.deleteMany(),
      prisma.session.deleteMany(),
      prisma.account.deleteMany(),
      prisma.verificationToken.deleteMany(),
      prisma.user.deleteMany(),
    ])
  return Response.json(
    {
      deleted: {
        accounts: accounts.count,
        devices: devices.count,
        sessions: sessions.count,
        users: users.count,
        verificationTokens: verificationTokens.count,
      },
    },
    { status: 200 },
  )
})

function requireCronSecret(request: Request) {
  const secret = process.env.CRON_SECRET?.trim()
  if (!secret) {
    throw new HTTPError(503, "CRON_SECRET is required.")
  }
  if (request.headers.get("authorization") !== `Bearer ${secret}`) {
    throw new HTTPError(401, "Unauthorized")
  }
}

export { GET }
