#!/bin/sh

set -eu

if [ ! -s /run/secrets/authorized_key ]; then
  echo "authorized SSH public key is missing" >&2
  exit 1
fi

install -d -m 0700 -o demo -g demo /home/demo/.ssh
install -m 0600 -o demo -g demo /run/secrets/authorized_key /home/demo/.ssh/authorized_keys
ssh-keygen -A

exec /usr/sbin/sshd -D -e
