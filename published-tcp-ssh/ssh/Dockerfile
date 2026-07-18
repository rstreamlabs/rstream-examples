FROM alpine:3.22

RUN apk add --no-cache openssh \
    && adduser -D -s /bin/sh demo \
    && passwd -d demo

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY sshd_config /etc/ssh/sshd_config

RUN chmod 0755 /usr/local/bin/entrypoint.sh

EXPOSE 22

HEALTHCHECK --interval=2s --timeout=1s --retries=15 CMD nc -z 127.0.0.1 22

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
