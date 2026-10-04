# Read-only runner diagnostics are distinct from the producer container's
# network path. Never publish arbitrary strings from the private CLI report.
{
  schemaVersion: 1,
  cliVersion: "1.32.10",
  scope: "runner-host",
  exitCode: $exit_code,
  reportAvailable: (.checks | type == "array"),
  checks: [
    .checks[]?
    | select(.name as $name | [
        "config", "context", "token", "control_plane_auth", "project",
        "engine_address", "dns", "tls", "quic_transport", "tunnel_transport", "engine"
      ] | index($name))
    | select(.status as $status | ["pass", "warn", "fail", "skip"] | index($status))
    | {name, status}
  ],
  tokenExpired: any(.checks[]?; .name == "token" and .message == "token has expired")
}
