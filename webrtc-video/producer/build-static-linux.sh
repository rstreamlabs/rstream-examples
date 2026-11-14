#!/usr/bin/env sh
set -eu

TARGETARCH="${TARGETARCH:-$(go env GOARCH)}"
GST_ROOT="${GST_ROOT:-/opt/gstreamer}"
GST_PC_DIR="${GST_ROOT}/lib/pkgconfig:${GST_ROOT}/lib/gstreamer-1.0/pkgconfig"

./ice-patch.sh

export CGO_ENABLED=1
export GOOS=linux
export GOARCH="${TARGETARCH}"
export CC="${CC:-cc}"
export PKG_CONFIG_PATH="${GST_PC_DIR}"
export PKG_CONFIG="pkg-config --static"

GST_FULL_CFLAGS="$(pkg-config --cflags gstreamer-full-1.0)"
GST_FULL_LDFLAGS="$(pkg-config --libs --static gstreamer-full-1.0)"

export CGO_CFLAGS="${CGO_CFLAGS:-} ${GST_FULL_CFLAGS}"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} ${GST_FULL_LDFLAGS}"

case "${1:-build}" in
  test)
    # Exercise the encoders shipped in the static artifact. Decoder-dependent
    # format/profile tests run in the full native producer job.
    exec go test -v -count=1 -timeout=8m -modfile=out/ice-patch/go.mod ./internal/media \
      -run '^Test(ClosedGStreamerSourceReleasesNativeCallbacks|ConcurrentStartWaitsForTheActualPipelineTransition|GStreamerStartPropagatesCancellation|GStreamerStartRejectsMissingContext|GStreamerStartFailureResetsPartialPipelineState|GStreamerSourceRestartsWithoutFalseEOS|GStreamerEncoderRequestsAKeyFrame)$'
    ;;
  build) ;;
  *) echo "usage: $0 [build|test]" >&2; exit 2 ;;
esac

mkdir -p /out

go build -modfile=out/ice-patch/go.mod \
  -buildvcs=false \
  -trimpath \
  -ldflags='-linkmode external -extldflags "-static" -s -w' \
  -o /out/webrtc-video-producer \
  ./cmd/webrtc-video-producer
