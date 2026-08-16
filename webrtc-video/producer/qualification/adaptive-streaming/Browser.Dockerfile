FROM node:24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        chromium \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /qualification
COPY package.json package-lock.json ./
RUN npm ci --omit=optional
COPY collect.mjs ./collect.mjs
COPY sample-receiver-udp.mjs ./sample-receiver-udp.mjs
COPY sample-host-cpu.sh /usr/local/bin/rstream-sample-host-cpu
COPY lib ./lib

ENTRYPOINT ["node", "/qualification/collect.mjs"]
