import assert from "node:assert/strict"
import test from "node:test"
import {
  deviceInventoryConfig,
  discoveredSource,
  discoveredSources,
} from "../src/lib/device-inventory.ts"

const id = "85a6703e-04de-42b6-93ac-c3b70c4cab51"
const tunnel = (changes = {}) => ({
  id: "tunnel-1",
  client_id: "client-1",
  project_id: "project-1",
  status: "online",
  protocol: "http",
  publish: true,
  token_auth: true,
  host: "video.example.com",
  name: "producer",
  labels: {
    app: "webrtc-video-platform",
    inventory: "discovered",
    device: id,
    "device-name": "Front camera",
  },
  ...changes,
})

test("discovery is opt-in, organization-only, and needs a stable project scope", () => {
  assert.deepEqual(deviceInventoryConfig({}), {
    mode: "managed",
    remember: true,
    projectId: "",
  })
  const config = {
    DEVICE_INVENTORY_MODE: "discovered",
    DEVICE_ACCESS_MODE: "organization",
    RSTREAM_PROJECT_ID: "project-1",
  }
  assert.equal(deviceInventoryConfig(config).remember, true)
  assert.equal(
    deviceInventoryConfig({
      ...config,
      DEVICE_DISCOVERY_HISTORY_ENABLED: "false",
    }).remember,
    false,
  )
  for (const changes of [
    { DEVICE_ACCESS_MODE: "user" },
    { RSTREAM_PROJECT_ID: " " },
    { DEVICE_DISCOVERY_HISTORY_ENABLED: "yes" },
  ])
    assert.throws(() => deviceInventoryConfig({ ...config, ...changes }))
})

test("stable identity survives a renamed source and a new tunnel session", () => {
  const first = discoveredSource(tunnel(), "project-1")
  const reconnected = tunnel({ id: "tunnel-2", name: "new-session" })
  reconnected.labels["device-name"] = "Rear camera"
  const second = discoveredSource(reconnected, "project-1")
  assert.equal(first.id, second.id)
  assert.equal(second.name, "Rear camera")
  delete reconnected.labels["device-name"]
  assert.equal(
    discoveredSource(reconnected, "project-1").name,
    `Device ${id.slice(0, 8)}`,
  )
})

test("unrelated, unauthenticated and foreign-project tunnels cannot become video devices", () => {
  for (const changes of [
    { project_id: "other" },
    { project_id: undefined },
    { status: "offline" },
    { protocol: "webtty" },
    { publish: false },
    { token_auth: false },
    { token_auth: undefined },
    { rstream_auth: true },
    { host: "user@example.com/path" },
    { host: "localhost" },
    { labels: { ...tunnel().labels, device: "../../secret" } },
    { labels: { ...tunnel().labels, inventory: "managed" } },
    { labels: { ...tunnel().labels, app: "another-application" } },
  ])
    assert.equal(
      discoveredSource(tunnel(changes), "project-1"),
      null,
      JSON.stringify(changes),
    )
})

test("display metadata has bounded UTF-8 size and cannot contain control characters", () => {
  for (const name of ["", "  ", "a\nb", "a\u202eb", "é".repeat(41)])
    assert.equal(
      discoveredSource(
        tunnel({ labels: { ...tunnel().labels, "device-name": name } }),
        "project-1",
      ),
      null,
    )
  assert.equal(
    discoveredSource(
      tunnel({ labels: { ...tunnel().labels, "device-name": "é".repeat(40) } }),
      "project-1",
    ).name.length,
    40,
  )
})

test("ambiguous active source IDs fail closed instead of choosing the latest tunnel", () => {
  const result = discoveredSources(
    [tunnel(), tunnel({ id: "tunnel-2" })],
    "project-1",
  )
  assert.equal(result.sources.size, 0)
  assert.deepEqual([...result.conflicts], [id])
})
