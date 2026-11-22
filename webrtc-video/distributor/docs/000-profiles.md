# Choose a distribution profile

Return to the [distributor overview](../README.md) for the application path. Run commands from `webrtc-video/distributor`, unless a step changes directory.

## Delivery profiles

| Profile               | Device uplinks | Producer leg                               | Viewer leg                     | Use it when                                                         |
| --------------------- | -------------: | ------------------------------------------ | ------------------------------ | ------------------------------------------------------------------- |
| Direct                | one per viewer | TWCC, NACK/RTX, FlexFEC, bounded pacer     | same end-to-end session        | one viewer needs the shortest path and end-to-end adaptation        |
| MediaMTX native pull  |     one shared | NACK/RTX and TWCC with fixed source pacing | MediaMTX NACK and TWCC         | a static source accepts the reduced MediaMTX 1.21.3 offer           |
| MediaMTX with adapter |     one shared | TWCC, NACK/RTX, FlexFEC, bounded pacer     | independent MediaMTX NACK/TWCC | a product needs dynamic sources, complete source repair and fan-out |

The native profile is intentionally retained as an interoperability option. It
removes the adapter when its smaller feature set and static source contract are
enough. The producer must opt into the bounded MediaMTX-native offer profile;
strict profiles do not relax their WHEP validation. Draft 04 requires
`rtcp-mux-only` and one common `msid` on the active media sections; MediaMTX
1.21.3 emits neither in its source offer and its player does not complete a `406`
counter-offer exchange. The opt-in accepts only those two known differences,
continues to require BUNDLE and RTCP multiplexing, and keeps fixed source
encoding/pacing for that session. RTX is negotiated when offered (including
MediaMTX 1.21.3); FlexFEC is absent. The adapter profile keeps the
strict producer contract and is the reference product path.

MediaMTX exposure is independent of these profiles. A public deployment gives
the browser an HTTPS MediaMTX URL. A private deployment publishes the same
WHEP listener with an authenticated rstream HTTP tunnel. In both cases media
uses ICE over UDP or TURN; the HTTP endpoint carries signaling, not RTP.

### Native pull for one static source

Native pull uses the same producer binary with an explicit compatibility
setting:

```yaml
web:
  whep:
    allowMediaMTXNativeOffer: true
```

Point one MediaMTX path at that producer. `wheps://` selects WHEP over HTTPS;
when the producer tunnel enforces rstream edge authentication, place its
short-lived connect token in `whepBearerToken`.

```yaml
paths:
  camera:
    source: wheps://producer.example/whep
    whepBearerToken: "short-lived-rstream-connect-token"
    sourceOnDemand: true
```

This profile is deliberately static: MediaMTX receives one source URL in its
configuration and pulls it on first demand. It is useful for a small fixed
deployment, but it does not provide the platform's per-device resolver or
automatic direct fallback. The sample retains fixed encoding and pacing for
this native source session. Adaptive encoding, optional source presets and
FlexFEC use the adapter profile; RTX alone does not enable these features.

The adapter terminates source repair before publishing a fresh downstream RTP
flow. Source transport-wide sequence numbers never cross into the MediaMTX
leg; MediaMTX and each browser establish their own feedback and retransmission
state. This separation prevents feedback from one viewer from controlling the
device uplink used by every viewer.

The adapter offers Constrained Baseline, Baseline, Main and High H.264 profiles
through level 4.2 in packetization mode 1. It preserves the negotiated source
profile and level in the MediaMTX offer; it does not decode or transcode the
stream. Sources outside this envelope need an explicitly qualified profile,
and downstream viewers must support the selected rendition.

The current distributor carries one H.264 rendition. Downstream TWCC describes
each viewer leg, but MediaMTX does not transcode that rendition or propagate a
viewer estimate back to the shared producer encoder. Fan-out is therefore
qualified only for viewers whose path can sustain the selected source profile.
Use direct delivery when one constrained viewer needs end-to-end encoder
adaptation. Heterogeneous audiences need a measured rendition ladder or
external transcoding workers before this boundary can be raised.
