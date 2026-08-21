#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
environment_file=$2
previous_dir="$deployment_dir/.previous"

if [[ ! -f "$deployment_dir/docker-compose.yml" ]]; then
  printf 'missing compose file: %s/docker-compose.yml\n' "$deployment_dir" >&2
  exit 66
fi
if [[ ! -f "$environment_file" ]]; then
  printf 'missing environment file: %s\n' "$environment_file" >&2
  exit 66
fi

compose=(docker compose --project-name lingow --env-file "$environment_file" --file "$deployment_dir/docker-compose.yml")

rollback_on_failure() {
  local status=$?
  trap - EXIT
  if (( status == 0 )); then
    exit 0
  fi
  if [[ ! -f "$previous_dir/.env.production" || ! -f "$previous_dir/docker-compose.yml" || ! -f "$previous_dir/deploy.sh" ]]; then
    printf 'deployment failed; no previous release is available for recovery\n' >&2
    exit "$status"
  fi
  printf 'deployment failed; restoring previous application release\n' >&2
  cp "$previous_dir/.env.production" "$environment_file"
  cp "$previous_dir/docker-compose.yml" "$deployment_dir/docker-compose.yml"
  cp "$previous_dir/deploy.sh" "$deployment_dir/deploy.sh"
  chmod 600 "$environment_file"
  chmod 700 "$deployment_dir/deploy.sh"
  if "${compose[@]}" config --quiet && "${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180; then
    printf 'previous application release restored; database schema was not rolled back\n' >&2
  else
    printf 'previous release recovery failed; database schema was not rolled back\n' >&2
  fi
  exit "$status"
}

trap rollback_on_failure EXIT

"${compose[@]}" config --quiet
"${compose[@]}" pull
"${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180
"${compose[@]}" ps
