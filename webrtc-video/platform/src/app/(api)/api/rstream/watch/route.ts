import { createWatchToken } from "@/lib/devices"
import { engine } from "@/lib/devices"
import { normalizeEngineAddress } from "@rstreamlabs/tunnels"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"
import { withUser } from "@/lib/next-auth"

const GET = withError(
  withUser(async (request: NextRequest, user) => {
    const [token, resolvedEngine] = await Promise.all([
      createWatchToken(user.access, request.signal),
      engine(request.signal),
    ])
    // Older Engines reject the client-list restriction. Use the server's
    // owner-filtered device list unless this token is explicitly denied access.
    const response = await fetch(
      `https://${normalizeEngineAddress(resolvedEngine)}/api/clients`,
      {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
        redirect: "error",
        signal: AbortSignal.any([request.signal, AbortSignal.timeout(5000)]),
      },
    )
    await response.body?.cancel()
    if (response.status !== 403)
      return new Response(null, {
        status: 204,
        headers: { "Cache-Control": "no-store" },
      })
    return Response.json(
      {
        auth: {
          token,
        },
        engine: resolvedEngine,
      },
      { status: 200, headers: { "Cache-Control": "no-store" } },
    )
  }),
)

export { GET }
