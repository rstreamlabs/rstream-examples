import { Buffer } from "node:buffer"
import { RstreamWebhookResource, type WebhookEvent } from "@rstreamlabs/tunnels"

const invalidWebhookMessage = "Invalid rstream webhook payload."

export async function verifiedRstreamWebhookEvent(
  rawBody: Buffer,
  signature: string,
  signingSecret: string,
): Promise<WebhookEvent> {
  try {
    return await new RstreamWebhookResource().event(
      rawBody,
      signature,
      signingSecret,
    )
  } catch {
    throw new Error(invalidWebhookMessage)
  }
}
