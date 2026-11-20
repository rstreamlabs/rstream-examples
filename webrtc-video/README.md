# Adaptive real-time WebRTC video

Stream a remote device to a browser. The Go producer captures and encodes with GStreamer, handles congestion feedback and retransmission, and publishes WebRTC signaling through an outbound rstream tunnel. ICE establishes the media path directly or through TURN.

| If you want to                               | Start here                                                             | First result                                                           |
| -------------------------------------------- | ---------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| Stream a device to a browser                 | [Producer quick start](producer/README.md#quick-start)                 | The embedded viewer plays a 720p H.264 test pattern from the producer. |
| Give signed-in users access to their devices | [Platform quick start](platform/README.md#run-the-application)         | A provisioned device comes online and plays in the Next.js dashboard.  |
| Serve several viewers over one device uplink | [Distributor quick start](distributor/README.md#run-with-the-platform) | Two browser sessions view one device through MediaMTX.                 |

The producer owns capture, encoding, congestion response and source metrics in all three paths. The platform owns authentication, device inventory and scoped credentials. MediaMTX distributes one rendition through independent downstream sessions. The adapter and native-pull options have different repair and source-control features. See [distribution profiles](distributor/docs/000-profiles.md).

## Build and test both Go components

From `webrtc-video`, use `make build` for the producer and adapter and `make verify` for their full checks. The Next.js application has [its own verification commands](platform/docs/003-testing.md).

| Task                                                              | Guide                                                                                                                                 |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Install media dependencies and package the producer               | [Build the producer](producer/docs/000-build.md)                                                                                      |
| Choose a capture source, codec or adaptation policy               | [Producer configuration](producer/docs/001-configuration.md)                                                                          |
| Set up organization access or discover existing devices           | [Platform setup](platform/docs/000-setup.md)                                                                                          |
| Add a separate distributor host                                   | [Distributor setup](distributor/docs/001-setup.md)                                                                                    |
| Enable recent recordings, distribution metrics or source controls | [Platform media capabilities](platform/docs/002-media.md)                                                                             |
| Exercise network impairment, recovery and fan-out                 | [Producer qualification](producer/qualification/adaptive-streaming/README.md), [Distributor testing](distributor/docs/004-testing.md) |
