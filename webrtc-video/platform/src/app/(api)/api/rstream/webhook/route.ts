import { HTTPError } from "@/lib/error"
import { Buffer } from "node:buffer"
import { recordDevicePresenceFromWebhookEvent } from "@/lib/rstream-webhook"
import { RstreamWebhookResource } from "@rstreamlabs/tunnels"
import { rstreamWebhookSigningSecret } from "@/lib/env"
import { type NextRequest } from "next/server"
import { withError } from "@/lib/error"

const signatureHeader = "rstream-signature"

const POST = withError(async (request: NextRequest) => {
  const signingSecret = rstreamWebhookSigningSecret()
  if (!signingSecret) {
    throw new HTTPError(
      503,
      "rstream webhook signing secret is not configured.",
    )
  }
  const signature = request.headers.get(signatureHeader)
  if (!signature) {
    throw new HTTPError(400, "Missing rstream-signature header.")
  }
  // Verify the exact raw payload before parsing the webhook event body.
  const rawBody = Buffer.from(await request.arrayBuffer())
  const event = await verifiedWebhookEvent(rawBody, signature, signingSecret)
  // Webhooks provide durable presence even when the browser watch stream is off.
  const result = await recordDevicePresenceFromWebhookEvent(event)
  return Response.json(
    {
      ok: true,
      event: event.type,
      device: result.deviceId,
      status: result.status,
      updated: result.updated,
    },
    { status: 200 },
  )
})

async function verifiedWebhookEvent(
  rawBody: Buffer,
  signature: string,
  signingSecret: string,
) {
  try {
    // The SDK validates the signature and returns the typed webhook event.
    return await new RstreamWebhookResource().event(
      rawBody,
      signature,
      signingSecret,
    )
  } catch (err) {
    throw new HTTPError(
      400,
      err instanceof Error ? err.message : "Invalid rstream webhook payload.",
    )
  }
}

export { POST }
