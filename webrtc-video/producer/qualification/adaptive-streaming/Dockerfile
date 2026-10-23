FROM node:24-bookworm AS web
WORKDIR /src
COPY producer/package.json producer/package-lock.json ./
RUN npm ci
COPY producer/tailwind.config.js producer/tsconfig.json ./
COPY producer/internal/web/embed ./internal/web/embed
COPY shared ../shared
RUN npm run build

FROM golang:1.27.3-bookworm AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        libgstreamer-plugins-base1.0-dev \
        libgstreamer1.0-dev \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY ice-patch /ice-patch

COPY producer/go.mod producer/go.sum producer/ice-patch.sh ./
RUN go mod download && ./ice-patch.sh
COPY producer/cmd ./cmd
COPY producer/internal ./internal
COPY producer/qualification/adaptive-streaming/cmd/local-control ./qualification/adaptive-streaming/cmd/local-control
COPY --from=web /src/internal/web/generated ./internal/web/generated
COPY producer/qualification/adaptive-streaming/latency/gstlatencystamp.c ./latency/
RUN mkdir -p /out \
    && cc -shared -fPIC -O2 -Wall -Wextra -Werror \
        -o /out/libgstlatencystamp.so latency/gstlatencystamp.c \
        $(pkg-config --cflags --libs gstreamer-video-1.0)
RUN CGO_ENABLED=1 go build -modfile=out/ice-patch/go.mod -trimpath -o /out/webrtc-video-producer ./cmd/webrtc-video-producer
RUN CGO_ENABLED=0 go build -modfile=out/ice-patch/go.mod -trimpath -o /out/local-control ./qualification/adaptive-streaming/cmd/local-control

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        gstreamer1.0-plugins-bad \
        gstreamer1.0-plugins-base \
        gstreamer1.0-plugins-ugly \
        iproute2 \
        jq \
    && rm -rf /var/lib/apt/lists/*
RUN useradd --create-home --uid 10001 rstream
WORKDIR /app
COPY --from=build /out/webrtc-video-producer ./webrtc-video-producer
COPY --from=build /src/out/ice-patch/provenance.json ./webrtc-video-producer.ice-patch.json
COPY --from=build /out/local-control ./local-control
COPY --from=build /out/libgstlatencystamp.so ./gstreamer-plugins/
ENV GST_PLUGIN_PATH=/app/gstreamer-plugins
COPY producer/config.test-pattern.h264.twcc-gcc.yaml ./config.yaml
COPY producer/qualification/adaptive-streaming/sample-host-cpu.sh /usr/local/bin/rstream-sample-host-cpu
USER rstream
ENTRYPOINT ["/app/webrtc-video-producer"]
CMD ["-config", "/app/config.yaml"]
