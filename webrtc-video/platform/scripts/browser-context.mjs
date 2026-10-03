import assert from "node:assert/strict"
import { networkInterfaces } from "node:os"

// The optional endpoint lets the same UI checks run in a disposable Linux
// Firefox when the host browser cannot establish local ICE connectivity.
// The caller supplies direct access to the fixture's loopback application port.
export async function launchBrowserContext(type, options = {}) {
  const endpoint = process.env.RSTREAM_FIREFOX_WS_ENDPOINT
  if (type.name() === "firefox" && !endpoint) {
    const loopback = Object.entries(networkInterfaces()).find(([, addresses]) =>
      addresses?.some(
        (address) => address.internal && address.family === "IPv4",
      ),
    )?.[0]
    assert.ok(loopback, "The local media fixture requires a loopback interface")
    options = {
      ...options,
      firefoxUserPrefs: {
        "media.peerconnection.ice.loopback": true,
        "media.peerconnection.ice.force_interface": loopback,
        "media.peerconnection.ice.obfuscate_host_addresses": false,
      },
    }
  }
  const browser =
    type.name() === "firefox" && endpoint
      ? await type.connect(endpoint, {
          timeout: 30000,
        })
      : await type.launch({ ...options, headless: true })
  try {
    const context = await browser.newContext()
    return { context, close: () => browser.close() }
  } catch (error) {
    await browser.close()
    throw error
  }
}
