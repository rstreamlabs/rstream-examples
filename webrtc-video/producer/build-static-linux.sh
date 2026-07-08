#!/usr/bin/env sh
set -eu

TARGETARCH="${TARGETARCH:-$(go env GOARCH)}"
GST_ROOT="${GST_ROOT:-/opt/gstreamer}"
GST_PC_DIR="${GST_ROOT}/lib/pkgconfig:${GST_ROOT}/lib/gstreamer-1.0/pkgconfig"

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

mkdir -p /out

go build \
  -buildvcs=false \
  -trimpath \
  -ldflags='-linkmode external -extldflags "-static" -s -w' \
  -o /out/webrtc-video-producer \
  ./cmd/webrtc-video-producer
