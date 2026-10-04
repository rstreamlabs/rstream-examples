FROM node:24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        chromium \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /qualification
COPY producer/qualification/adaptive-streaming/package.json producer/qualification/adaptive-streaming/package-lock.json ./
RUN npm ci --omit=optional
COPY producer/qualification/adaptive-streaming/collect.mjs ./collect.mjs
COPY producer/qualification/adaptive-streaming/viewer.ts ./viewer.ts
COPY producer/qualification/adaptive-streaming/sample-receiver-udp.mjs ./sample-receiver-udp.mjs
COPY producer/qualification/adaptive-streaming/sample-host-cpu.sh /usr/local/bin/rstream-sample-host-cpu
COPY producer/qualification/adaptive-streaming/lib ./lib
COPY producer/qualification/adaptive-streaming/latency ./latency
COPY shared /shared
RUN ./node_modules/.bin/esbuild viewer.ts --bundle --format=esm --outfile=viewer.js
RUN ./node_modules/.bin/esbuild latency/probe-entry.mjs --bundle --format=iife --outfile=latency-probe.js

ENTRYPOINT ["node", "/qualification/collect.mjs"]
