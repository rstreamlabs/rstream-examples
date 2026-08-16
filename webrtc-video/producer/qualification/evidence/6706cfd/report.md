# Adaptive real-time video qualification — `6706cfd`

All three reference scenarios passed on source revision
`6706cfdf830ded88a718bc35c6b4a94a0a69e089`, with a clean producer tree and the
same pinned Chromium receiver. The profile is 1920×1080 H.264 at 30 fps with
TWCC/GCC adaptation, NACK/RTX, FlexFEC, a 200 ms playout-delay hint, and a
2–8 Mbit/s encoder envelope.

![Direct adaptive bitrate response](./direct-flexfec/adaptive-bitrate.svg)

## Results

| Scenario          | Assertions | Healthy baseline                                               | Controlled impairment                                         | Recovery                                                      |
| ----------------- | ---------: | -------------------------------------------------------------- | ------------------------------------------------------------- | ------------------------------------------------------------- |
| Direct reference  |      50/50 | 7.5 Mbit/s encoder, 10.56 Mbit/s received, 30.0 fps, 0% frozen | 2 Mbit/s encoder, 2.97 Mbit/s received, 30.0 fps, 0% frozen   | 5.5 Mbit/s encoder, 7.42 Mbit/s received, 30.1 fps, 0% frozen |
| rstream relay     |      49/49 | 2 Mbit/s encoder, 2.99 Mbit/s received, 30.0 fps, 0% frozen    | 2 Mbit/s encoder, 2.95 Mbit/s received, 29.4 fps, 7.2% frozen | 2 Mbit/s encoder, 3.04 Mbit/s received, 30.2 fps, 0.6% frozen |
| QUIC/ICE mobility |      53/53 | 2 Mbit/s encoder, 3.04 Mbit/s received, 30.0 fps, 0% frozen    | 2 Mbit/s encoder, 2.96 Mbit/s received, 29.7 fps, 2.7% frozen | 2 Mbit/s encoder, 3.01 Mbit/s received, 30.2 fps, 0% frozen   |

The controlled impairment combines a 4 Mbit/s path, 120 ms one-way delay,
30 ms jitter, and 2% random packet loss. Received bitrate includes protection
and retransmission traffic, so it is expected to exceed the encoder media
target when FlexFEC and RTX are active.

The direct reference reaches the configured quality ceiling on the local test
path and demonstrates the complete downshift and recovery cycle. The relay
path available during this run settled around a 2 Mbit/s encoder target. The
relay result therefore qualifies continuity, latency, repair, and visual
quality at measured path capacity; it does not claim that 2 Mbit/s is an
rstream throughput ceiling.

![rstream relay adaptive bitrate response](./relay-flexfec/adaptive-bitrate.svg)

## Mobility continuity

The mobility run moved the producer between two isolated interfaces with
different source addresses while rstream signaling was forced over QUIC. The
existing session produced one fresh Trickle ICE candidate and one ICE restart,
switched the selected candidate pair once, and recovered playback within
1.006 seconds. It retained one peer connection and one signaling WebSocket,
with no WebSocket close event.

The same session then completed the bandwidth, latency, loss, and recovery
phases. This matters because a successful interface switch would be incomplete
evidence if the congestion controller or media pacer remained degraded after
the switch.

## Evidence integrity

The network impairment targets only the selected WebRTC transport. Each media
host records scheduler cadence and kernel UDP counters so local CPU starvation
or socket overflow cannot be mislabeled as network loss. The sender also
exports queue residence, complete-frame admission drops, RTX/FlexFEC timing,
controller feedback, QP, and key-frame recovery counters.

Before the selected runs, the harness rejected attempts with an excessive RTT
spike and an artificial recovery freeze. Investigation showed that deleting a
populated `netem` qdisc discarded its delayed packets. The final harness first
drains that queue for one measured second at zero delay and zero loss, requires
the queue to reach zero without an added drop, removes traffic control, and
only then starts the unshaped recovery phase. Every selected run proves this
drain independently.

Full reports and machine-readable assertions:

- [direct reference](./direct-flexfec/summary.md)
- [rstream relay](./relay-flexfec/summary.md)
- [QUIC/ICE mobility](./relay-mobility/summary.md)
