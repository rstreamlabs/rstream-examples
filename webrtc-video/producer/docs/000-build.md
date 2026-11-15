# Build the producer

Return to the [producer overview](../README.md) for the application path. Run commands from `webrtc-video/producer`, unless a step changes directory.

## Requirements

Before running the example, install the `rstream` CLI, create a free `rstream` account, create a project, and select that project locally. The sample expects an active CLI context:

```bash
rstream login
rstream project use <project-endpoint>
```

For local development you need Go `1.27.4` or newer, a C compiler, `pkg-config`,
and a GStreamer installation that includes the development files and the
elements required by the selected pipeline. Use Node.js `24 LTS` for the
embedded viewer build. Node.js and npm are only required with `make build`,
`make run`, or `make test`.

When using the Next.js platform provisioning profile, the producer does not
serve the embedded viewer UI. Use `make build-provisioning` for that mode; it
skips npm entirely and builds the binary with `web.viewer.enabled: false`
configs in mind.

The H.264 profiles use `videotestsrc`, `videoconvert`, `x264enc`, `h264parse`, and `appsink`. The AV1 profiles use `av1enc` and `av1parse` on top of the same structure.

The bundled profiles have specific codec and receiver limits; see [Media compatibility](001-configuration.md#media-compatibility) before changing a pipeline.

### macOS

```bash
brew install go pkg-config gstreamer
```

The [Homebrew GStreamer formula](https://formulae.brew.sh/formula/gstreamer) includes the plugin collections. Install Node.js 24 with npm only when embedding the viewer; the [Node.js download page](https://nodejs.org/en/download) provides version-specific installers. The producer-only build does not need Node.js:

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

Please run the sample inside WSL2. This repository does not ship a Windows-native distribution target for this example.

## Build and distribution

For local development:

```bash
make build
make run
```

`make build` embeds the local viewer UI and therefore requires Node.js and npm.
For the Next.js platform provisioning profile, use the no-viewer build:

```bash
make build-provisioning
```

For a local-only run:

```bash
make run-local
```

For tests:

```bash
make test
```

The source-format tests encode and decode real H.264 frames, so the development
runtime also needs `avdec_h264` from GStreamer's libav plugin (`gstreamer1.0-libav`
on Debian/Ubuntu, included above). Check it with `gst-inspect-1.0 avdec_h264`.
This decoder is a test dependency; the producer's runtime pipeline and static
distribution do not decode the transmitted video.

The repository also ships Docker-based static packaging targets for Linux:

```bash
make dist-linux-amd64
make dist-linux-arm64
make dist
```

Artifacts are written to `dist/linux-amd64` and `dist/linux-arm64`.

Those targets build a static Linux binary linked against a statically packaged `gstreamer-full` toolchain. The Docker build compiles the GStreamer subset needed by the sample, including `x264`, `libaom`, the parsers, and the `appsink` path, then links the Go binary against that toolchain with `musl`.

The default bundle includes the test-pattern source. Camera sources such as
`libcamerasrc` require their platform libraries and plugins: use the native
GStreamer build on that device, or extend the static toolchain before packaging.
Copying a camera YAML file alone does not add its capture plugin to the binary.

The practical outcome is a standalone executable you can copy to a target machine without asking that machine to install the full GStreamer development stack first. In other words, `make dist` is the path you use when you want to build once and then copy the resulting binary to a remote device.

That static toolchain is defined in `build-gstreamer-static-linux.sh`. If you change the reference pipelines and introduce new elements or plugins, update that script as well. Otherwise the local development setup may keep working while the static distribution build silently stops matching the pipeline you intend to run.

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

If the process cannot connect to the `rstream` engine server, verify the current CLI context with `rstream login` and `rstream project use <project-endpoint>`, then inspect `~/.rstream/config.yaml`.

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
