import assert from "node:assert/strict";
import test from "node:test";
import { PathStability, pathMatchesPolicy } from "../lib/path.mjs";

test("requires one selected candidate path to remain stable", () => {
  const stability = new PathStability(3000);
  const first = sample("relay", "prflx", 56001, 2401);
  const second = sample("relay", "prflx", 56002, 2402);
  assert.equal(stability.observe(first, 0), false);
  assert.equal(stability.observe(first, 2999), false);
  assert.equal(stability.observe(second, 3000), false);
  assert.equal(stability.observe(second, 6000), true);
});

test("resets path stability while disconnected", () => {
  const stability = new PathStability(1000);
  const connected = sample("relay", "prflx", 56001, 2401);
  const disconnected = { ...connected, peerConnectionState: "disconnected" };
  assert.equal(stability.observe(connected, 0), false);
  assert.equal(stability.observe(disconnected, 1000), false);
  assert.equal(stability.observe(connected, 2000), false);
});

test("checks direct and relay policies from both candidate sides", () => {
  assert.equal(
    pathMatchesPolicy(sample("relay", "relay", 1, 2), "relay"),
    true,
  );
  assert.equal(
    pathMatchesPolicy(sample("relay", "prflx", 1, 2), "relay"),
    false,
  );
  assert.equal(
    pathMatchesPolicy(sample("host", "relay", 1, 2), "relay"),
    false,
  );
  assert.equal(
    pathMatchesPolicy(sample("host", "srflx", 1, 2), "direct"),
    true,
  );
  assert.equal(
    pathMatchesPolicy(sample("host", "relay", 1, 2), "direct"),
    false,
  );
});

function sample(localType, remoteType, localPort, remotePort) {
  return {
    localCandidateAddress: "192.0.2.1",
    localCandidatePort: localPort,
    localCandidateProtocol: "udp",
    localCandidateType: localType,
    peerConnectionState: "connected",
    producerLocalCandidateProtocol: "udp",
    producerLocalCandidateType: remoteType === "relay" ? "relay" : "host",
    producerLocalCandidateURL:
      remoteType === "relay" ? "turn:turn.example.com:3478?transport=udp" : "",
    producerLocalRelayProtocol: remoteType === "relay" ? "udp" : "",
    producerRemoteCandidateProtocol: "udp",
    producerRemoteCandidateType: localType,
    remoteCandidateAddress: "198.51.100.1",
    remoteCandidatePort: remotePort,
    remoteCandidateProtocol: "udp",
    remoteCandidateType: remoteType,
  };
}
