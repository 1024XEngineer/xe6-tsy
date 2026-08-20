#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
environment_file=$2

if [[ ! -f "$deployment_dir/docker-compose.yml" ]]; then
  printf 'missing compose file: %s/docker-compose.yml\n' "$deployment_dir" >&2
  exit 66
fi
if [[ ! -f "$environment_file" ]]; then
  printf 'missing environment file: %s\n' "$environment_file" >&2
  exit 66
fi

compose=(docker compose --project-name lingow --env-file "$environment_file" --file "$deployment_dir/docker-compose.yml")

"${compose[@]}" config --quiet
"${compose[@]}" pull
"${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180
"${compose[@]}" ps
