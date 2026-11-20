# Build the producer

Return to the [producer overview](../README.md) for the application path. Run commands from `webrtc-video/producer`, unless a step changes directory.

## Requirements

Use Go 1.27.4 or newer, Git, a C compiler, `pkg-config` and the GStreamer headers and libraries. The viewer build and its tests use Node.js 24 with npm. The no-viewer build uses Go and the native media dependencies.

The H.264 test pattern uses `videotestsrc`, `videoconvert`, `x264enc`, `h264parse` and `appsink`. The AV1 test pattern uses `av1enc` and `av1parse` in place of the H.264 encoder and parser. Check the elements for your profile with `gst-inspect-1.0`. Review the codec and receiver limits before changing a pipeline. [Media compatibility](001-configuration.md#media-compatibility).

### macOS

```bash
brew install go pkg-config gstreamer
```

Install Xcode Command Line Tools for the C compiler. The [Homebrew GStreamer formula](https://formulae.brew.sh/formula/gstreamer) includes the plugin collections. For the embedded viewer, install [Node.js 24 with npm](https://nodejs.org/en/download).

### Ubuntu / Debian

Install Go `1.27.4` or newer using the [Go installation instructions](https://go.dev/doc/install)
and check `go version`. The distribution's default `golang` package can be
older than the module requires. Then install the native dependencies:

```bash
sudo apt update
sudo apt install -y \
  build-essential \
  gstreamer1.0-plugins-bad \
  gstreamer1.0-plugins-base \
  gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-ugly \
  gstreamer1.0-libav \
  gstreamer1.0-tools \
  libgstreamer-plugins-base1.0-dev \
  libgstreamer1.0-dev \
  pkg-config
```

Install [Node.js 24 LTS with npm](https://nodejs.org/en/download) only if you
want the producer binary to serve the embedded viewer UI. Check the versions
before building; distribution packages may provide an older Node.js release:

```bash
node --version
npm --version
```

For the Raspberry Pi camera profiles, also install the platform's libcamera
GStreamer plugin (`gstreamer1.0-libcamera` on Debian) and check
`gst-inspect-1.0 libcamerasrc`. Camera access and its drivers are requirements
of those profiles, separate from the test-pattern source.

### Windows

Use WSL2 and follow the Linux dependency instructions for its distribution.

## Build and distribution

The Makefiles and Docker builds for the producer and distributor apply the required [ICE concurrency patch](../../ICE-PATCH.md).

| Command                   | Output                                                                                                                           |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `make build`              | `./webrtc-video-producer` with the browser viewer embedded. See the [quick start](../README.md#quick-start) for launch commands. |
| `make build-provisioning` | `./webrtc-video-producer` for profiles with `web.viewer.enabled: false`. This build skips npm.                                   |
| `make test`               | TypeScript checks and Go tests.                                                                                                  |
| `make verify`             | Formatting, vet, Go and race tests, GCC regression, no-viewer tests and a build.                                                 |
| `make dist-linux-amd64`   | Static binary in `dist/linux-amd64`. Requires Docker with Buildx.                                                                |
| `make dist-linux-arm64`   | Static binary in `dist/linux-arm64`. Requires Docker with Buildx.                                                                |
| `make dist`               | Both static Linux builds.                                                                                                        |

The source-format tests encode and decode real H.264 frames. Check for `avdec_h264` with `gst-inspect-1.0 avdec_h264`. On Debian/Ubuntu it comes from `gstreamer1.0-libav`. The producer runtime and static package encode outbound video.

Those targets build a static Linux binary linked against a statically packaged `gstreamer-full` toolchain. The Docker build compiles the GStreamer subset needed by the sample, including `x264`, `libaom`, the parsers, and the `appsink` path, then links the Go binary against that toolchain with `musl`.

The default bundle includes the test-pattern source. Camera sources such as
`libcamerasrc` require their platform libraries and plugins: use the native
GStreamer build on that device, or extend the static toolchain before packaging.
Copying a camera YAML file alone does not add its capture plugin to the binary.

Copy the static executable to the matching Linux architecture. If a profile adds a new GStreamer element, include its plugin in [the static toolchain build](../build-gstreamer-static-linux.sh) before packaging it.

The packaged sources are GStreamer 1.28.9, libaom 3.15.3 and the immutable x264
commit recorded in that script. Both release archives are verified with SHA-256
before compilation. A custom Docker `GST_VERSION` or `AOM_VERSION` build argument
also requires its corresponding `GST_SHA256` or `AOM_SHA256`; `X264_GIT_REF`
accepts a full commit hash. These pins identify the media sources, not a claim
that every external Alpine package or generated binary is bit-for-bit reproducible.

## Troubleshooting

`make build`, `make build-provisioning`, and `make test` run a preflight check
before compiling Go. If `pkg-config` or the GLib/GStreamer development files
are missing, the build prints the exact missing pkg-config packages and points
to the installation commands in this guide.

If the process fails with `failed to create the GStreamer pipeline`, one or more configured elements are missing. Install the required plugins or adapt `media.pipeline` to the elements available on the target machine.

If the process cannot connect to the `rstream` engine server, verify the current CLI context with `rstream login` and `rstream project use your-project-endpoint`, then inspect `~/.rstream/config.yaml`.

If TURN credential generation fails, verify the current project endpoint and
credential permissions. Local PAT/APP derivation requires `turn.relay.allocate`;
managed issuance requires `turn.credentials.create`. Certificate-only devices
use the discovered mTLS Engine API directly, without a Control plane token. Grant
Engine operations separately for the published tunnel. The local TURN provider
retains one SDK client across renewals, so discovery is cached in memory and the
existing CLI context/certificate setup is sufficient. It closes that client and
cancels any pending refresh when the process stops. Remote provisioning continues
to request TURN credentials from the configured product API.

If the public URL opens but no video appears, start with the runtime log, the selected GStreamer pipeline, browser autoplay and permissions, TURN reachability from both peers, and the availability of the chosen encoder on the target machine.
