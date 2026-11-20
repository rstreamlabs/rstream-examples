# Test the media path

Return to the [producer overview](../README.md) for the application path. Run commands from `webrtc-video/producer`, unless a step changes directory.

## Testing degraded connectivity

Use the [adaptive H.264 profile](../config.test-pattern.h264.twcc-gcc.yaml) on a test producer. On Linux, `tc netem` can shape the UDP media path. The commands below change the entire selected interface. Use a disposable interface without an existing custom root queue and keep a separate path to administer the producer.

Replace `wlan0` with the test interface carrying media to the viewer or TURN relay.

```bash
sudo tc qdisc add dev wlan0 root netem delay 80ms 20ms loss 3% rate 2mbit
```

Tighten the path during playback.

```bash
sudo tc qdisc change dev wlan0 root netem delay 160ms 40ms loss 6% rate 1mbit
```

Remove the test queue after observing the result.

```bash
sudo tc qdisc del dev wlan0 root netem
```

When adaptive bitrate is enabled, the useful signals on the viewer page are `TWCC target` and `Encoder target`. The transport estimate should move first, and the encoder target should then follow according to the configured update policy. Use the [adaptive qualification](../qualification/adaptive-streaming/README.md) to shape only the selected UDP path and collect comparable evidence.
