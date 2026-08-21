#!/bin/sh
set -eu

image=${1:-rstream-video-distributor:local}
container="rstream-video-distributor-smoke-$$"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM
docker run -d \
  --name "$container" \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  "$image" >/dev/null

user=$(docker inspect --format '{{.Config.User}}' "$container")
if [ "$user" != "rstream" ] && [ "$user" != "10001" ]; then
  printf 'container user is %s, want rstream or 10001\n' "$user" >&2
  exit 1
fi
readonly_root=$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container")
capabilities=$(docker inspect --format '{{json .HostConfig.CapDrop}}' "$container")
security=$(docker inspect --format '{{json .HostConfig.SecurityOpt}}' "$container")
if [ "$readonly_root" != "true" ] || [ "$capabilities" != '["ALL"]' ] || [ "$security" != '["no-new-privileges"]' ]; then
  printf 'container hardening is incomplete: read-only=%s cap-drop=%s security=%s\n' "$readonly_root" "$capabilities" "$security" >&2
  exit 1
fi

attempt=0
while [ "$attempt" -lt 20 ]; do
  state=$(docker inspect --format '{{.State.Status}}' "$container")
  if [ "$state" != "running" ]; then
    docker logs "$container" >&2
    printf 'container state is %s, want running\n' "$state" >&2
    exit 1
  fi
  if docker exec "$container" wget -q -T 2 -O /tmp/metrics http://127.0.0.1:9998/metrics \
    && docker exec "$container" wget -q -T 2 -O /tmp/adapter-metrics http://127.0.0.1:9999/metrics; then
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done

if [ "$attempt" -eq 20 ]; then
  docker logs "$container" >&2
  printf 'metrics listener did not become ready\n' >&2
  exit 1
fi

docker exec "$container" grep -q '^paths ' /tmp/metrics
docker exec "$container" grep -q '^rstream_video_distributor_children' /tmp/adapter-metrics
healthcheck=$(docker inspect --format '{{if .Config.Healthcheck}}{{json .Config.Healthcheck.Test}}{{else}}null{{end}}' "$container")
if [ "$healthcheck" = "null" ] || [ "$healthcheck" = "[]" ]; then
  printf 'container image has no healthcheck\n' >&2
  exit 1
fi
