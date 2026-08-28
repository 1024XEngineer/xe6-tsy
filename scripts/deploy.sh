#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 && $# -ne 3 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file>\n' "$0" >&2
  printf '       %s <deployment-directory> <release-directory> <smoke-session-id|--dynamic-smoke|--no-smoke>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
staged_release=false
if [[ $# -eq 3 ]]; then
  release_dir=$2
  environment_file="$release_dir/.env.production"
  staged_release=true
  if [[ "$3" == "--no-smoke" ]]; then
    smoke_enabled=false
    if [[ "${DEPLOY_ALLOW_NO_SMOKE:-false}" != "true" ]]; then
      printf 'staged releases require an authenticated smoke test\n' >&2
      exit 64
    fi
  elif [[ "$3" == "--dynamic-smoke" ]]; then
    smoke_session_id=''
    smoke_enabled=true
    smoke_dynamic=true
  else
    smoke_session_id=$3
    smoke_enabled=true
    smoke_dynamic=false
  fi
else
  release_dir=$deployment_dir
  environment_file=$2
  smoke_enabled=false
  smoke_dynamic=false
fi
previous_dir="$deployment_dir/.previous"
proxy_dir="$deployment_dir/proxy"
previous_proxy_dir="$previous_dir/proxy"
project_name="${DEPLOY_PROJECT_NAME:-lingow}"

if [[ ! "$project_name" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  printf 'invalid DEPLOY_PROJECT_NAME\n' >&2
  exit 64
fi

if [[ -n "${DEPLOY_EXPECTED_ENV:-}" ]]; then
  deployment_env="$(awk -F= '$1 == "LINGOW_DEPLOY_ENV" { print substr($0, index($0, "=") + 1); exit }' "$environment_file" 2>/dev/null || true)"
  if [[ "$deployment_env" != "$DEPLOY_EXPECTED_ENV" ]]; then
    printf 'deployment environment mismatch: expected %s, got %s\n' "$DEPLOY_EXPECTED_ENV" "${deployment_env:-<missing>}" >&2
    exit 64
  fi
fi

install -d -m 700 "$deployment_dir"
lock_file="$deployment_dir/.deploy.lock"
if command -v flock >/dev/null 2>&1; then
  exec {lock_fd}>"$lock_file"
  if ! flock -n "$lock_fd"; then
    printf 'another deployment is already running for %s\n' "$deployment_dir" >&2
    exit 75
  fi
fi

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
  if [[ "${smoke_dynamic:-false}" == true ]]; then
    smoke_access_token=''
  else
    smoke_access_token=$(cat | tr -d '\r\n')
  fi
  if [[ "${smoke_dynamic:-false}" != true && -z "$smoke_access_token" ]]; then
    printf 'missing smoke access token\n' >&2
    exit 64
  fi
fi

compose=(docker compose --project-name "$project_name" --env-file "$environment_file" --file "$release_dir/docker-compose.yml")

snapshot_current_release() {
  # An older manually-created release may not have the helper scripts yet;
  # its Compose and environment files are still enough to restore the app.
  if [[ ! -f "$deployment_dir/.env.production" || ! -f "$deployment_dir/docker-compose.yml" ]]; then
    return 0
  fi
  install -d -m 700 "$previous_dir"
  cp "$deployment_dir/.env.production" "$previous_dir/.env.production"
  cp "$deployment_dir/docker-compose.yml" "$previous_dir/docker-compose.yml"
  if [[ -f "$deployment_dir/deploy.sh" ]]; then
    cp "$deployment_dir/deploy.sh" "$previous_dir/deploy.sh"
  fi
  if [[ -f "$deployment_dir/deploy-smoke.sh" ]]; then
    cp "$deployment_dir/deploy-smoke.sh" "$previous_dir/deploy-smoke.sh"
  fi
  if [[ -f "$deployment_dir/observe.sh" ]]; then
    cp "$deployment_dir/observe.sh" "$previous_dir/observe.sh"
  fi
  if [[ -f "$proxy_dir/nginx.conf" && -f "$proxy_dir/docker-compose.yml" ]]; then
    install -d -m 700 "$previous_proxy_dir"
    cp "$proxy_dir/nginx.conf" "$previous_proxy_dir/nginx.conf"
    cp "$proxy_dir/docker-compose.yml" "$previous_proxy_dir/docker-compose.yml"
  fi
}

run_proxy_compose() {
  local compose_file=$1
  local proxy_env=(env "DEPLOY_PROJECT_NAME=$project_name" "PROXY_CERT_DIR=$proxy_dir/certs")
  local proxy_compose=(docker compose --project-name "${project_name}-proxy" --env-file "$environment_file" --file "$compose_file")
  "${proxy_env[@]}" "${proxy_compose[@]}" "${@:2}"
}

sync_proxy_release() {
  if [[ ! -f "$release_dir/proxy/nginx.conf" || ! -f "$release_dir/proxy/docker-compose.yml" ]]; then
    return 0
  fi
  if [[ ! -d "$proxy_dir/certs" ]]; then
    printf 'proxy certificate directory is missing: %s\n' "$proxy_dir/certs" >&2
    return 66
  fi
  install -d -m 700 "$proxy_dir"
  cp "$release_dir/proxy/nginx.conf" "$proxy_dir/nginx.conf"
  cp "$release_dir/proxy/docker-compose.yml" "$proxy_dir/docker-compose.yml"
  run_proxy_compose "$release_dir/proxy/docker-compose.yml" config --quiet
  run_proxy_compose "$release_dir/proxy/docker-compose.yml" up --detach --remove-orphans --wait --wait-timeout 60
}

restore_previous_proxy() {
  if [[ ! -f "$previous_proxy_dir/nginx.conf" || ! -f "$previous_proxy_dir/docker-compose.yml" ]]; then
    return 0
  fi
  install -d -m 700 "$proxy_dir"
  cp "$previous_proxy_dir/nginx.conf" "$proxy_dir/nginx.conf"
  cp "$previous_proxy_dir/docker-compose.yml" "$proxy_dir/docker-compose.yml"
  if ! run_proxy_compose "$proxy_dir/docker-compose.yml" config --quiet ||
    ! run_proxy_compose "$proxy_dir/docker-compose.yml" up --detach --remove-orphans --wait --wait-timeout 60; then
    printf 'previous proxy release recovery failed\n' >&2
  fi
}

rollback_on_failure() {
  local status=$?
  trap - EXIT
  if (( status == 0 )); then
    exit 0
  fi
  if [[ ! -f "$previous_dir/.env.production" || ! -f "$previous_dir/docker-compose.yml" ]]; then
    printf 'deployment failed; no previous release is available for recovery\n' >&2
    # A first deployment has no stable application to restore. Remove only the
    # candidate containers and network; named data volumes are never removed.
    "${compose[@]}" down --remove-orphans || true
    exit "$status"
  fi
  printf 'deployment failed; restoring previous application release\n' >&2
  cp "$previous_dir/.env.production" "$environment_file"
  cp "$previous_dir/docker-compose.yml" "$deployment_dir/docker-compose.yml"
  if [[ -f "$previous_dir/deploy.sh" ]]; then
    cp "$previous_dir/deploy.sh" "$deployment_dir/deploy.sh"
  fi
  if [[ -f "$previous_dir/deploy-smoke.sh" ]]; then
    cp "$previous_dir/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
  fi
  if [[ -f "$previous_dir/observe.sh" ]]; then
    cp "$previous_dir/observe.sh" "$deployment_dir/observe.sh"
  fi
  cp "$previous_dir/.env.production" "$deployment_dir/.env.production"
  chmod 600 "$environment_file" "$deployment_dir/.env.production"
  [[ ! -f "$deployment_dir/deploy.sh" ]] || chmod 700 "$deployment_dir/deploy.sh"
  [[ ! -f "$deployment_dir/observe.sh" ]] || chmod 700 "$deployment_dir/observe.sh"
  previous_compose=(docker compose --project-name "$project_name" --env-file "$deployment_dir/.env.production" --file "$deployment_dir/docker-compose.yml")
  if "${previous_compose[@]}" config --quiet && "${previous_compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180; then
    printf 'previous application release restored; database schema was not rolled back\n' >&2
  else
    printf 'previous release recovery failed; database schema was not rolled back\n' >&2
  fi
  restore_previous_proxy
  exit "$status"
}

snapshot_current_release
trap rollback_on_failure EXIT

"${compose[@]}" config --quiet
"${compose[@]}" pull
"${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180
sync_proxy_release
"${compose[@]}" ps

if [[ "$smoke_enabled" == true ]]; then
  if [[ "${smoke_dynamic:-false}" == true ]]; then
    printf '\n' | bash "$release_dir/deploy-smoke.sh" "$release_dir" "$environment_file"
  else
    printf '%s\n' "$smoke_access_token" | bash "$release_dir/deploy-smoke.sh" "$release_dir" "$environment_file" "$smoke_session_id"
  fi
fi

if [[ "$staged_release" == true ]]; then
  cp "$release_dir/.env.production" "$deployment_dir/.env.production"
  cp "$release_dir/docker-compose.yml" "$deployment_dir/docker-compose.yml"
  cp "$release_dir/deploy.sh" "$deployment_dir/deploy.sh"
  cp "$release_dir/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
  if [[ -f "$release_dir/observe.sh" ]]; then
    cp "$release_dir/observe.sh" "$deployment_dir/observe.sh"
  fi
  if [[ -d "$release_dir/proxy" ]]; then
    install -d -m 700 "$deployment_dir/proxy"
    cp "$release_dir/proxy/nginx.conf" "$deployment_dir/proxy/nginx.conf"
    cp "$release_dir/proxy/docker-compose.yml" "$deployment_dir/proxy/docker-compose.yml"
  fi
  chmod 600 "$deployment_dir/.env.production"
  chmod 700 "$deployment_dir/deploy.sh" "$deployment_dir/deploy-smoke.sh"
  [[ ! -f "$deployment_dir/observe.sh" ]] || chmod 700 "$deployment_dir/observe.sh"
fi
