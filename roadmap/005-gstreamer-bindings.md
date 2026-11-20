# Review the GStreamer binding migration

The video producer retains the qualified `go-gst` and `go-glib` 1.4 source pins. Upstream retracted that handwritten binding line and moved to a different release scheme, so replacing it requires a source migration.

Review the replacement API and license before changing the pins. Verify capture, encoding, cancellation and shutdown on both supported native architectures, including the statically packaged producer.
