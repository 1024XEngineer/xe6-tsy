#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s <deployment-directory>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
previous_dir="$deployment_dir/.previous"
proxy_dir="$deployment_dir/proxy"
previous_proxy_dir="$previous_dir/proxy"
project_name="${DEPLOY_PROJECT_NAME:-lingow}"

if [[ ! "$project_name" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  printf 'invalid DEPLOY_PROJECT_NAME\n' >&2
  exit 64
fi
if [[ ! -f "$previous_dir/.env.production" || ! -f "$previous_dir/docker-compose.yml" ]]; then
  if [[ "${ROLLBACK_CLEAN_FIRST_RELEASE:-false}" != true ]]; then
    printf 'previous release is missing; refusing rollback without explicit first-release cleanup\n' >&2
    exit 66
  fi
  if [[ ! -f "$deployment_dir/.env.production" || ! -f "$deployment_dir/docker-compose.yml" ]]; then
    printf 'current release is missing; cannot clean first release\n' >&2
    exit 66
  fi
  if [[ -n "${DEPLOY_EXPECTED_ENV:-}" ]]; then
    current_env="$(awk -F= '$1 == "LINGOW_DEPLOY_ENV" { print substr($0, index($0, "=") + 1); exit }' "$deployment_dir/.env.production" 2>/dev/null || true)"
    if [[ "$current_env" != "$DEPLOY_EXPECTED_ENV" ]]; then
      printf 'current release environment mismatch: expected %s, got %s\n' \
        "$DEPLOY_EXPECTED_ENV" "${current_env:-<missing>}" >&2
      exit 64
    fi
  fi
  compose=(docker compose --project-name "$project_name" --env-file "$deployment_dir/.env.production" --file "$deployment_dir/docker-compose.yml")
  "${compose[@]}" config --quiet
  "${compose[@]}" down --remove-orphans
  printf 'first release containers removed; named data volumes were preserved\n'
  exit 0
fi

restore_previous_proxy() {
  if [[ ! -f "$previous_proxy_dir/nginx.conf" || ! -f "$previous_proxy_dir/docker-compose.yml" ]]; then
    return 0
  fi
  install -d -m 700 "$proxy_dir"
  cp "$previous_proxy_dir/nginx.conf" "$proxy_dir/nginx.conf"
  cp "$previous_proxy_dir/docker-compose.yml" "$proxy_dir/docker-compose.yml"
  if [[ ! -d "$proxy_dir/certs" ]]; then
    printf 'proxy certificate directory is missing; application rollback continues\n' >&2
    return 0
  fi
  proxy_env=(env "DEPLOY_PROJECT_NAME=$project_name" "PROXY_CERT_DIR=$proxy_dir/certs")
  proxy_compose=(docker compose --project-name "${project_name}-proxy" --env-file "$deployment_dir/.env.production" --file "$proxy_dir/docker-compose.yml")
  "${proxy_env[@]}" "${proxy_compose[@]}" config --quiet
  "${proxy_env[@]}" "${proxy_compose[@]}" up --detach --remove-orphans --wait --wait-timeout 60
}

for required in .env.production docker-compose.yml; do
  if [[ ! -f "$previous_dir/$required" ]]; then
    printf 'previous release is missing %s\n' "$previous_dir/$required" >&2
    exit 66
  fi
done

if [[ -n "${DEPLOY_EXPECTED_ENV:-}" ]]; then
  previous_env="$(awk -F= '$1 == "LINGOW_DEPLOY_ENV" { print substr($0, index($0, "=") + 1); exit }' "$previous_dir/.env.production" 2>/dev/null || true)"
  if [[ "$previous_env" != "$DEPLOY_EXPECTED_ENV" ]]; then
    printf 'previous release environment mismatch: expected %s, got %s\n' \
      "$DEPLOY_EXPECTED_ENV" "${previous_env:-<missing>}" >&2
    exit 64
  fi
fi

install -d -m 700 "$deployment_dir"
if command -v flock >/dev/null 2>&1; then
  exec {lock_fd}>"$deployment_dir/.deploy.lock"
  flock -n "$lock_fd" || { printf 'another deployment is already running\n' >&2; exit 75; }
fi

cp "$previous_dir/.env.production" "$deployment_dir/.env.production"
cp "$previous_dir/docker-compose.yml" "$deployment_dir/docker-compose.yml"
if [[ -f "$previous_dir/deploy.sh" ]]; then
  cp "$previous_dir/deploy.sh" "$deployment_dir/deploy.sh"
  chmod 700 "$deployment_dir/deploy.sh"
fi
if [[ -f "$previous_dir/deploy-smoke.sh" ]]; then
  cp "$previous_dir/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
  chmod 700 "$deployment_dir/deploy-smoke.sh"
fi
if [[ -f "$previous_dir/observe.sh" ]]; then
  cp "$previous_dir/observe.sh" "$deployment_dir/observe.sh"
  chmod 700 "$deployment_dir/observe.sh"
fi
if [[ -f "$previous_proxy_dir/nginx.conf" && -f "$previous_proxy_dir/docker-compose.yml" ]]; then
  install -d -m 700 "$proxy_dir"
  cp "$previous_proxy_dir/nginx.conf" "$proxy_dir/nginx.conf"
  cp "$previous_proxy_dir/docker-compose.yml" "$proxy_dir/docker-compose.yml"
fi
chmod 600 "$deployment_dir/.env.production"

compose=(docker compose --project-name "$project_name" --env-file "$deployment_dir/.env.production" --file "$deployment_dir/docker-compose.yml")
"${compose[@]}" config --quiet
"${compose[@]}" up --detach --remove-orphans --wait --wait-timeout 180
restore_previous_proxy
printf 'previous application release restored\n'
