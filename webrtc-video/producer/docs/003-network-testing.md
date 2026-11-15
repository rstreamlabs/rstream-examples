# Test the media path

Return to the [producer overview](../README.md) for the application path. Run commands from `webrtc-video/producer`, unless a step changes directory.

## Testing degraded connectivity

If you want to validate adaptive bitrate, shape the real device-side network path instead of only throttling page load. On Linux, `tc netem` is the most useful baseline because it affects the actual UDP media traffic.

Apply shaping on the interface that carries viewer traffic, for example `wlan0`:

```bash
sudo tc qdisc add dev wlan0 root netem delay 80ms 20ms loss 3% rate 2mbit
```

Tighten the path further:

```bash
sudo tc qdisc change dev wlan0 root netem delay 160ms 40ms loss 6% rate 1mbit
```

Remove the shaping afterwards:

```bash
sudo tc qdisc del dev wlan0 root netem
```

When adaptive bitrate is enabled, the useful signals on the viewer page are `TWCC target` and `Encoder target`. The transport estimate should move first, and the encoder target should then follow within the configured update interval.
