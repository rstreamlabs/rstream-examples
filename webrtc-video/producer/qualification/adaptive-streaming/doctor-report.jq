# Read-only runner diagnostics are distinct from the producer container's
# network path. Never publish arbitrary strings from the private CLI report.
def transport_error_class:
  if type != "string" then "unavailable"
  elif test("context canceled|context cancelled"; "i") then "canceled"
  elif test("deadline exceeded|i/o timeout|timed out|timeout:"; "i") then "timeout"
  elif test("connection refused"; "i") then "connection-refused"
  elif test("network is unreachable|no route to host"; "i") then "network-unreachable"
  elif test("cannot assign requested address|no such network interface|unknown network interface|bind:"; "i") then "local-bind"
  elif test("x509:|certificate|tls:"; "i") then "tls-handshake"
  else "unclassified"
  end;

def address_family:
  if test("^([0-9]{1,3}\\.){3}[0-9]{1,3}$") and
     (split(".") | all(.[]; tonumber <= 255)) then "ipv4"
  elif contains(":") and test("^[0-9a-fA-F:.]+(%[a-zA-Z0-9_.-]+)?$") then "ipv6"
  else "unrecognized"
  end;

{
  schemaVersion: 2,
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
  tokenExpired: any(.checks[]?; .name == "token" and .message == "token has expired"),
  # Classifications describe standard error text, not a proven root cause.
  # Neither the original error nor an address/hostname leaves the report.
  transportDiagnostics: [
    .checks[]?
    | select(.name == "tls" or .name == "quic_transport")
    | select(.status == "fail" or .status == "warn")
    | {name, status, errorClass: (.details.error | transport_error_class)}
  ],
  dnsAddressFamilies: [
    .checks[]?
    | select(.name == "dns" and .status == "pass")
    | .details.addresses
    | select(type == "string")
    | split(",")[]
    | gsub("^\\s+|\\s+$"; "")
    | address_family
  ] | unique,
  transportSelection: [
    .checks[]?
    | select(.name == "tunnel_transport")
    | .details
    | {
        configured: (.configuredMode | select(. == "auto" or . == "tls" or . == "quic")),
        selected: (.selectedMode | select(. == "none" or . == "tls" or . == "quic"))
      }
  ]
}
