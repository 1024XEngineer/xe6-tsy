#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 && $# -ne 3 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file>\n' "$0" >&2
  printf '       %s <deployment-directory> <release-directory> <smoke-session-id|--no-smoke>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
staged_release=false
if [[ $# -eq 3 ]]; then
  release_dir=$2
  environment_file="$release_dir/.env.production"
  staged_release=true
  if [[ "$3" == --no-smoke ]]; then
    smoke_enabled=false
  else
    smoke_session_id=$3
    smoke_enabled=true
  fi
else
  release_dir=$deployment_dir
  environment_file=$2
  smoke_enabled=false
fi
previous_dir="$deployment_dir/.previous"

if [[ ! -f "$release_dir/docker-compose.yml" ]]; then
  printf 'missing compose file: %s/docker-compose.yml\n' "$release_dir" >&2
  exit 66
fi
if [[ ! -f "$environment_file" ]]; then
  printf 'missing environment file: %s\n' "$environment_file" >&2
  exit 66
fi
if [[ "$smoke_enabled" == true && ! -f "$release_dir/deploy-smoke.sh" ]]; then
  printf 'missing smoke script: %s/deploy-smoke.sh\n' "$release_dir" >&2
  exit 66
fi
if [[ "$smoke_enabled" == true ]]; then
  smoke_access_token=$(cat)
  if [[ -z "$smoke_access_token" ]]; then
    printf 'missing smoke access token\n' >&2
    exit 64
  fi
fi

compose=(docker compose --project-name lingow --env-file "$environment_file" --file "$release_dir/docker-compose.yml")

snapshot_current_release() {
  if [[ ! -f "$deployment_dir/.env.production" || ! -f "$deployment_dir/docker-compose.yml" || ! -f "$deployment_dir/deploy.sh" ]]; then
    return 0
  fi
  install -d -m 700 "$previous_dir"
  cp "$deployment_dir/.env.production" "$previous_dir/.env.production"
  cp "$deployment_dir/docker-compose.yml" "$previous_dir/docker-compose.yml"
  cp "$deployment_dir/deploy.sh" "$previous_dir/deploy.sh"
  if [[ -f "$deployment_dir/deploy-smoke.sh" ]]; then
    cp "$deployment_dir/deploy-smoke.sh" "$previous_dir/deploy-smoke.sh"
  fi
}

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
  if [[ -f "$previous_dir/deploy-smoke.sh" ]]; then
    cp "$previous_dir/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
  fi
  cp "$previous_dir/.env.production" "$deployment_dir/.env.production"
  chmod 600 "$environment_file" "$deployment_dir/.env.production"
  chmod 700 "$deployment_dir/deploy.sh"
  previous_compose=(docker compose --project-name lingow --env-file "$deployment_dir/.env.production" --file "$deployment_dir/docker-compose.yml")
  if "${previous_compose[@]}" config --quiet && "${previous_compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180; then
    printf 'previous application release restored; database schema was not rolled back\n' >&2
  else
    printf 'previous release recovery failed; database schema was not rolled back\n' >&2
  fi
  exit "$status"
}

snapshot_current_release
trap rollback_on_failure EXIT

"${compose[@]}" config --quiet
"${compose[@]}" pull
"${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180
"${compose[@]}" ps

if [[ "$smoke_enabled" == true ]]; then
  printf '%s\n' "$smoke_access_token" | bash "$release_dir/deploy-smoke.sh" "$release_dir" "$environment_file" "$smoke_session_id"
fi

if [[ "$staged_release" == true ]]; then
  cp "$release_dir/.env.production" "$deployment_dir/.env.production"
  cp "$release_dir/docker-compose.yml" "$deployment_dir/docker-compose.yml"
  cp "$release_dir/deploy.sh" "$deployment_dir/deploy.sh"
  cp "$release_dir/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
  chmod 600 "$deployment_dir/.env.production"
  chmod 700 "$deployment_dir/deploy.sh" "$deployment_dir/deploy-smoke.sh"
fi
