FROM node:24-bookworm AS web
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY tailwind.config.js tsconfig.json ./
COPY internal/web/embed ./internal/web/embed
RUN npm run build

FROM golang:1.26.6-bookworm AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        libgstreamer-plugins-base1.0-dev \
        libgstreamer1.0-dev \
        pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/web/generated ./internal/web/generated
RUN CGO_ENABLED=1 go build -trimpath -o /out/webrtc-video-producer ./cmd/webrtc-video-producer

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
COPY config.test-pattern.h264.twcc-gcc.yaml ./config.yaml
COPY qualification/adaptive-streaming/sample-host-cpu.sh /usr/local/bin/rstream-sample-host-cpu
USER rstream
ENTRYPOINT ["/app/webrtc-video-producer"]
CMD ["-config", "/app/config.yaml"]
