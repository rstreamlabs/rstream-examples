import assert from "node:assert/strict";
import test from "node:test";
import {
  parseTURNURL,
  selectImpairmentTarget,
} from "../lib/impairment-target.mjs";

test("selects direct peer transport by address without pinning its ICE port", () => {
  const target = selectImpairmentTarget(
    {
      mediaDestinationAddress: "192.0.2.10",
      mediaDestinationPort: 5004,
      mediaDestinationProtocol: "udp",
      producerICEPath: { localCandidateType: "host" },
    },
    "direct",
  );
  assert.deepEqual(target, {
    address: "192.0.2.10",
    family: 4,
    port: 5004,
    protocol: "udp",
    matchDestinationPort: false,
    relayProtocol: null,
    scope: "peer-webrtc-transport-address",
    source: "192.0.2.10",
  });
});

test("selects the physical TURN TLS transport for a producer relay", () => {
  const target = selectImpairmentTarget(
    {
      producerICEPath: {
        localCandidateAddress: "198.51.100.42",
        localCandidateType: "relay",
        localCandidateURL: "turns:turn.example.com:5350?transport=tcp",
        localRelayProtocol: "tls",
      },
    },
    "relay",
  );
  assert.deepEqual(target, {
    address: "198.51.100.42",
    family: 4,
    port: 5350,
    protocol: "tcp",
    matchDestinationPort: true,
    relayProtocol: "tls",
    scope: "producer-turn-transport",
    selectedCandidateAddress: "198.51.100.42",
    source: "turns:turn.example.com:5350?transport=tcp",
  });
});

test("falls back to TURN DNS when selected relay address is unavailable", () => {
  const target = selectImpairmentTarget(
    {
      producerICEPath: {
        localCandidateType: "relay",
        localCandidateURL: "turn:turn.example.com:3478?transport=udp",
        localRelayProtocol: "udp",
      },
    },
    "relay",
  );
  assert.deepEqual(target, {
    address: "turn.example.com",
    family: 0,
    port: 3478,
    protocol: "udp",
    matchDestinationPort: true,
    relayProtocol: "udp",
    scope: "producer-turn-transport",
    selectedCandidateAddress: null,
    source: "turn:turn.example.com:3478?transport=udp",
  });
});

test("rejects a malformed selected relay address", () => {
  assert.throws(
    () =>
      selectImpairmentTarget(
        {
          producerICEPath: {
            localCandidateAddress: "not-an-address",
            localCandidateType: "relay",
            localCandidateURL: "turn:turn.example.com:3478?transport=udp",
            localRelayProtocol: "udp",
          },
        },
        "relay",
      ),
    /invalid selected producer relay address/,
  );
});

test("parses IPv6 DTLS TURN URLs and defaults standard ports", () => {
  assert.deepEqual(parseTURNURL("turns:[2001:db8::1]?transport=udp"), {
    host: "2001:db8::1",
    port: 5349,
    relayProtocol: "dtls",
  });
  assert.deepEqual(parseTURNURL("turn:turn.example.com?transport=udp"), {
    host: "turn.example.com",
    port: 3478,
    relayProtocol: "udp",
  });
});

test("rejects inconsistent selected relay diagnostics", () => {
  assert.throws(
    () =>
      selectImpairmentTarget(
        {
          producerICEPath: {
            localCandidateType: "relay",
            localCandidateURL: "turn:turn.example.com:3478?transport=tcp",
            localRelayProtocol: "udp",
          },
        },
        "relay",
      ),
    /does not match/,
  );
});
