FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rstream-video-distributor ./cmd/rstream-video-distributor

FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS mediamtx

RUN apk add --no-cache python3
WORKDIR /build
COPY mediamtx-build ./mediamtx-build
RUN python3 mediamtx-build/build.py --output /mediamtx

FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 rstream \
    && adduser -S -D -H -u 10001 -G rstream rstream \
    && install -d -m 0555 /etc/rstream
COPY --from=mediamtx --chmod=0555 /mediamtx /usr/local/bin/mediamtx
COPY --from=mediamtx --chmod=0444 /mediamtx.build.json /usr/local/share/mediamtx.build.json
COPY --from=build --chmod=0555 /out/rstream-video-distributor /usr/local/bin/rstream-video-distributor
COPY --chmod=0444 mediamtx.yml /etc/rstream/mediamtx.yml

USER rstream
EXPOSE 8189/udp 8889/tcp 9998/tcp 9999/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -T 4 -O /dev/null http://127.0.0.1:9998/metrics && wget -q -T 4 -O /dev/null http://127.0.0.1:9999/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/rstream-video-distributor", "host", "/usr/local/bin/mediamtx"]
CMD ["/etc/rstream/mediamtx.yml"]
