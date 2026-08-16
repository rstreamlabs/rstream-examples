FROM postgres:18-alpine

RUN apk add --no-cache openssl \
    && install -d -m 0700 -o postgres -g postgres /var/lib/postgresql/tls \
    && openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 2 \
        -subj /CN=postgres-qualification \
        -keyout /var/lib/postgresql/tls/server.key \
        -out /var/lib/postgresql/tls/server.crt \
    && chown postgres:postgres \
        /var/lib/postgresql/tls/server.key \
        /var/lib/postgresql/tls/server.crt \
    && chmod 0600 /var/lib/postgresql/tls/server.key
