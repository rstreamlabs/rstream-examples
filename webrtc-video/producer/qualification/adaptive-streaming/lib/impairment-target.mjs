import { isIP } from "node:net";

export function selectImpairmentTarget(ready, pathKind) {
  if (!new Set(["direct", "relay"]).has(pathKind)) {
    throw new Error(`path kind must be direct or relay, got ${pathKind}`);
  }
  const producerPath = ready?.producerICEPath;
  if (pathKind === "relay" && producerPath?.localCandidateType === "relay") {
    const parsed = parseTURNURL(producerPath.localCandidateURL);
    const selectedCandidateAddress = String(
      producerPath.localCandidateAddress || "",
    ).trim();
    if (selectedCandidateAddress && isIP(selectedCandidateAddress) === 0) {
      throw new Error(
        `invalid selected producer relay address ${JSON.stringify(selectedCandidateAddress)}`,
      );
    }
    const relayProtocol = normalizeRelayProtocol(
      producerPath.localRelayProtocol,
    );
    if (parsed.relayProtocol !== relayProtocol) {
      throw new Error(
        `selected relay protocol ${relayProtocol} does not match ${parsed.relayProtocol} from ${producerPath.localCandidateURL}`,
      );
    }
    return {
      address: selectedCandidateAddress || parsed.host,
      family: isIP(selectedCandidateAddress || parsed.host),
      port: parsed.port,
      protocol:
        relayProtocol === "tcp" || relayProtocol === "tls" ? "tcp" : "udp",
      matchDestinationPort: true,
      relayProtocol,
      scope: "producer-turn-transport",
      selectedCandidateAddress: selectedCandidateAddress || null,
      source: producerPath.localCandidateURL,
    };
  }
  const address = String(ready?.mediaDestinationAddress || "").trim();
  const port = normalizePort(ready?.mediaDestinationPort);
  const protocol = String(
    ready?.mediaDestinationProtocol || "udp",
  ).toLowerCase();
  if (!address) {
    throw new Error("collector did not provide a media destination address");
  }
  if (protocol !== "udp") {
    throw new Error(`direct peer media must use UDP, got ${protocol}`);
  }
  return {
    address,
    family: isIP(address),
    port,
    protocol,
    matchDestinationPort: false,
    relayProtocol: null,
    scope: "peer-webrtc-transport-address",
    source: address,
  };
}

export function parseTURNURL(value) {
  const raw = String(value || "").trim();
  const match = raw.match(
    /^(turns?):(?:\/\/)?(\[[^\]]+\]|[^:/?]+)(?::([0-9]+))?(?:\?([^#]*))?$/i,
  );
  if (!match) {
    throw new Error(`invalid TURN URL ${JSON.stringify(raw)}`);
  }
  const scheme = match[1].toLowerCase();
  const host = match[2].replace(/^\[|\]$/g, "");
  const port = normalizePort(match[3] || (scheme === "turns" ? 5349 : 3478));
  const parameters = new URLSearchParams(match[4] || "");
  const transport = (parameters.get("transport") || "udp").toLowerCase();
  let relayProtocol;
  if (scheme === "turn" && transport === "udp") {
    relayProtocol = "udp";
  } else if (scheme === "turn" && transport === "tcp") {
    relayProtocol = "tcp";
  } else if (scheme === "turns" && transport === "udp") {
    relayProtocol = "dtls";
  } else if (scheme === "turns" && transport === "tcp") {
    relayProtocol = "tls";
  } else {
    throw new Error(`unsupported TURN URL ${JSON.stringify(raw)}`);
  }
  return { host, port, relayProtocol };
}

function normalizeRelayProtocol(value) {
  const protocol = String(value || "")
    .trim()
    .toLowerCase();
  if (!new Set(["udp", "tcp", "dtls", "tls"]).has(protocol)) {
    throw new Error(`invalid selected relay protocol ${JSON.stringify(value)}`);
  }
  return protocol;
}

function normalizePort(value) {
  const port = Number(value);
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`invalid network port ${JSON.stringify(value)}`);
  }
  return port;
}
