#!/usr/bin/env bash

set -euo pipefail

deployment_dir=${1:?usage: ensure-turn-config.sh <deployment-directory>}
config_path=${TURN_CONFIG_PATH:-${deployment_dir}/dependencies/turnserver.conf}
container_name=${TURN_CONTAINER_NAME:-lingow-dependencies-turn-1}
compose_file=${TURN_COMPOSE_FILE:-${deployment_dir}/dependencies/docker-compose.yml}
compose_env=${TURN_COMPOSE_ENV_FILE:-${deployment_dir}/dependencies/.env}
turn_image=${TURN_IMAGE:-coturn/coturn:4.6.3}

if [[ ! -f "$config_path" ]]; then
  printf 'TURN config not found at %s; skipping\n' "$config_path"
  exit 0
fi

# coturn runs as nobody in the dependency compose. The deployment user may not
# be able to read this file after the ownership change, so do the repair in a
# short-lived root container instead of depending on host sudo access.
docker run --rm --user 0 \
  --volume "${config_path}:/mnt/turnserver.conf:rw" \
  --entrypoint sh "$turn_image" \
  -c 'chown 65534:65534 /mnt/turnserver.conf && chmod 600 /mnt/turnserver.conf'

if ! docker container inspect "$container_name" >/dev/null 2>&1; then
  printf 'TURN container %s is not present; permission repair completed\n' "$container_name"
  exit 0
fi

needs_recreate=false
if ! docker exec "$container_name" test -r /etc/coturn/turnserver.conf; then
  needs_recreate=true
elif docker exec "$container_name" sh -c \
  'test -f /var/tmp/turn.log && grep -q "Cannot find config file: /etc/coturn/turnserver.conf" /var/tmp/turn.log'; then
  needs_recreate=true
fi

if [[ "$needs_recreate" != true ]]; then
  printf 'TURN config is readable and loaded by %s\n' "$container_name"
  exit 0
fi

if [[ ! -f "$compose_file" || ! -f "$compose_env" ]]; then
  printf 'TURN config needs a recreate, but dependency Compose files are missing\n' >&2
  exit 66
fi

docker compose --env-file "$compose_env" --file "$compose_file" up -d --force-recreate turn
if ! docker exec "$container_name" test -r /etc/coturn/turnserver.conf; then
  printf 'TURN config is still unreadable after automatic recreate\n' >&2
  exit 70
fi
printf 'TURN container %s recreated with a readable config\n' "$container_name"
