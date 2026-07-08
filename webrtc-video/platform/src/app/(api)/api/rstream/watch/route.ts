import { createWatchToken } from "@/lib/devices"
import { engine } from "@/lib/devices"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"

const GET = withError(
  withUser(async (_request: NextRequest, user) => {
    const [token, resolvedEngine] = await Promise.all([
      createWatchToken(user.id),
      engine(),
    ])
    return Response.json(
      {
        auth: {
          token,
        },
        engine: resolvedEngine,
      },
      { status: 200 },
    )
  }),
)

export { GET }
