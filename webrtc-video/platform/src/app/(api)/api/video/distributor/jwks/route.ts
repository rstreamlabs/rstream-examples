import { mediaMTXJWKS } from "@/lib/video-distributor"
import { videoDistributorMode } from "@/lib/video-distributor"

export function GET() {
  if (videoDistributorMode() !== "mediamtx") {
    return Response.json(
      { error: "MediaMTX distribution is disabled." },
      { status: 404 },
    )
  }
  return Response.json(mediaMTXJWKS(), {
    headers: { "Cache-Control": "public, max-age=300" },
    status: 200,
  })
}
