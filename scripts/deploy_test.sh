#!/usr/bin/env bash

set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"

write_fake_docker() {
  printf '%s\n' '#!/usr/bin/env bash' \
    'set -euo pipefail' \
    'case "$*" in' \
    '  *realtime-ticket*) printf '\''{"ticket":"ticket"}'\'' ;;' \
    '  *webrtc/config*)' \
    '    if [[ "${FAKE_TURN:-0}" == 1 ]]; then' \
    '      printf '\''{"session_id":"session","ice_servers":[{"urls":["turns:turn.example"]}]}'\''' \
    '    else' \
    '      printf '\''{"session_id":"session","ice_servers":[{"urls":["stun:stun.example"]}]}'\''' \
    '    fi' \
    '    ;;' \
    'esac' \
    > "$test_root/bin/docker"
  chmod 700 "$test_root/bin/docker"
}

copy_script() {
  tr -d '\r' < "$1" > "$2"
  chmod 700 "$2"
}

prepare_release() {
  local release_dir=$2
  mkdir -p "$release_dir"
  printf 'new\n' > "$release_dir/.env.production"
  printf 'new compose\n' > "$release_dir/docker-compose.yml"
  copy_script "$repo_dir/scripts/deploy.sh" "$release_dir/deploy.sh"
  copy_script "$repo_dir/scripts/deploy-smoke.sh" "$release_dir/deploy-smoke.sh"
}

run_release() {
  local deployment_dir=$1
  local release_dir=$2
  local turn=$3
  printf 'smoke-token\n' | FAKE_TURN="$turn" PATH="$test_root/bin:$PATH" \
    bash "$release_dir/deploy.sh" "$deployment_dir" "$release_dir" session
}

write_fake_docker

first_deployment="$test_root/first"
first_release="$first_deployment/.staging/candidate"
prepare_release "$first_deployment" "$first_release"
run_release "$first_deployment" "$first_release" 1
cmp "$first_release/.env.production" "$first_deployment/.env.production"
cmp "$first_release/docker-compose.yml" "$first_deployment/docker-compose.yml"
cmp "$first_release/deploy.sh" "$first_deployment/deploy.sh"
printf 'first deployment runs the staging release\n'

upgrade_deployment="$test_root/upgrade"
upgrade_release="$upgrade_deployment/.staging/candidate"
mkdir -p "$upgrade_deployment"
printf 'old\n' > "$upgrade_deployment/.env.production"
printf 'old compose\n' > "$upgrade_deployment/docker-compose.yml"
printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'if [[ $# -ne 2 ]]; then exit 64; fi' \
  'printf old-script-called > "$1/old-script-called"' \
  > "$upgrade_deployment/deploy.sh"
chmod 700 "$upgrade_deployment/deploy.sh"
prepare_release "$upgrade_deployment" "$upgrade_release"
run_release "$upgrade_deployment" "$upgrade_release" 1
if [[ -e "$upgrade_deployment/old-script-called" ]]; then
  printf 'old root deploy script was executed\n' >&2
  exit 1
fi
cmp "$upgrade_release/deploy.sh" "$upgrade_deployment/deploy.sh"
printf 'upgrade runs the staging release instead of the old root script\n'

failed_deployment="$test_root/failed"
failed_release="$failed_deployment/.staging/candidate"
mkdir -p "$failed_deployment"
printf 'old\n' > "$failed_deployment/.env.production"
printf 'old compose\n' > "$failed_deployment/docker-compose.yml"
copy_script "$repo_dir/scripts/deploy.sh" "$failed_deployment/deploy.sh"
copy_script "$repo_dir/scripts/deploy-smoke.sh" "$failed_deployment/deploy-smoke.sh"
prepare_release "$failed_deployment" "$failed_release"

set +e
run_release "$failed_deployment" "$failed_release" 0
status=$?
set -e
if [[ $status -eq 0 ]]; then
  printf 'expected smoke failure to fail deployment\n' >&2
  exit 1
fi
cmp "$failed_deployment/.env.production" <(printf 'old\n')
cmp "$failed_deployment/docker-compose.yml" <(printf 'old compose\n')
printf 'deployment smoke failure restores previous release\n'
