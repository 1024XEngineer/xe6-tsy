#!/usr/bin/env bash

set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"

write_fake_docker() {
  printf '%s\n' '#!/usr/bin/env bash' \
    'set -euo pipefail' \
    'printf "%s\n" "$*" >> "${FAKE_DOCKER_LOG:-/dev/null}"' \
    'case "$*" in' \
    '  *auth/anonymous*) printf '\''{"account":{"id":"acct_smoke"},"tokens":{"access_token":"dynamic-token","refresh_token":"refresh-token"}}'\'' ;;' \
    '  *auth/logout*) printf '\''{}'\'' ;;' \
    '  *voice-sessions/*/end*) printf '\''{}'\'' ;;' \
    '  *voice-sessions) printf '\''{"id":"vs_dynamic"}'\'' ;;' \
    '  */metrics*)' \
    '    count_file="${FAKE_METRICS_COUNT_FILE:-}"' \
    '    count=0' \
    '    if [[ -n "$count_file" && -f "$count_file" ]]; then count=$(cat "$count_file"); fi' \
    '    count=$((count + 1))' \
    '    if [[ -n "$count_file" ]]; then printf "%s" "$count" > "$count_file"; fi' \
    '    failures=0' \
    '    if [[ "${FAKE_METRICS_FAIL:-0}" == 1 && "$count" -gt 1 ]]; then failures=1; fi' \
    '    printf '\''{"provider_failures":{"asr":0,"assistant":0,"translation":0,"tts":0},"data_channel_failures":%s,"semantic_commands":{"interpretation_failures":0}}'\'' "$failures"' \
    '    ;;' \
    '  *realtime-ticket*) printf '\''{"ticket":"ticket"}'\'' ;;' \
    '  *webrtc/config*)' \
    '    session_id=session' \
    '    if [[ "${FAKE_DYNAMIC:-0}" == 1 ]]; then session_id=vs_dynamic; fi' \
    '    if [[ "${FAKE_TURN:-0}" == 1 ]]; then' \
    '      printf '\''{"session_id":"%s","ice_servers":[{"urls":["turns:turn.example"]}]}'\'' "$session_id"' \
    '    else' \
    '      printf '\''{"session_id":"%s","ice_servers":[{"urls":["stun:stun.example"]}]}'\'' "$session_id"' \
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
  copy_script "$repo_dir/scripts/observe.sh" "$release_dir/observe.sh"
}

run_release() {
  local deployment_dir=$1
  local release_dir=$2
  local turn=$3
  : > "$test_root/docker.log"
  printf 'smoke-token\n' | FAKE_TURN="$turn" FAKE_DOCKER_LOG="$test_root/docker.log" \
    DEPLOY_PROJECT_NAME=lingow-development PATH="$test_root/bin:$PATH" \
    bash "$release_dir/deploy.sh" "$deployment_dir" "$release_dir" session
}

run_dynamic_release() {
  local deployment_dir=$1
  local release_dir=$2
  : > "$test_root/docker.log"
  FAKE_TURN=1 FAKE_DYNAMIC=1 FAKE_DOCKER_LOG="$test_root/docker.log" \
    DEPLOY_PROJECT_NAME=lingow-development PATH="$test_root/bin:$PATH" \
    bash "$release_dir/deploy.sh" "$deployment_dir" "$release_dir" --dynamic-smoke
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

dynamic_deployment="$test_root/dynamic"
dynamic_release="$dynamic_deployment/.staging/candidate"
prepare_release "$dynamic_deployment" "$dynamic_release"
run_dynamic_release "$dynamic_deployment" "$dynamic_release"
grep -q -- 'auth/anonymous' "$test_root/docker.log"
grep -q -- 'voice-sessions' "$test_root/docker.log"
printf 'dynamic smoke creates and cleans up an isolated account/session\n'

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

no_smoke_deployment="$test_root/no-smoke"
no_smoke_release="$no_smoke_deployment/.staging/candidate"
prepare_release "$no_smoke_deployment" "$no_smoke_release"
set +e
env -u DEPLOY_ALLOW_NO_SMOKE DEPLOY_PROJECT_NAME=lingow-development \
  bash "$no_smoke_release/deploy.sh" "$no_smoke_deployment" "$no_smoke_release" --no-smoke
status=$?
set -e
if [[ "$status" -ne 64 ]]; then
  printf 'expected --no-smoke to require explicit opt-in, got %s\n' "$status" >&2
  exit 1
fi
DEPLOY_ALLOW_NO_SMOKE=true DEPLOY_PROJECT_NAME=lingow-development \
  FAKE_DOCKER_LOG="$test_root/docker.log" PATH="$test_root/bin:$PATH" \
  bash "$no_smoke_release/deploy.sh" "$no_smoke_deployment" "$no_smoke_release" --no-smoke
printf 'no-smoke requires explicit opt-in\n'

environment_deployment="$test_root/environment"
environment_release="$environment_deployment/.staging/candidate"
prepare_release "$environment_deployment" "$environment_release"
printf 'LINGOW_DEPLOY_ENV=production\n' > "$environment_release/.env.production"
set +e
DEPLOY_ALLOW_NO_SMOKE=true DEPLOY_EXPECTED_ENV=development \
  bash "$environment_release/deploy.sh" "$environment_deployment" "$environment_release" --no-smoke
status=$?
set -e
if [[ "$status" -ne 64 ]]; then
  printf 'expected deployment environment mismatch, got %s\n' "$status" >&2
  exit 1
fi
printf 'deployment environment mismatch is rejected\n'

if command -v flock >/dev/null 2>&1; then
  lock_deployment="$test_root/locked"
  lock_release="$lock_deployment/.staging/candidate"
  prepare_release "$lock_deployment" "$lock_release"
  mkdir -p "$lock_deployment"
  exec 9>"$lock_deployment/.deploy.lock"
  flock -n 9
  set +e
  DEPLOY_ALLOW_NO_SMOKE=true DEPLOY_PROJECT_NAME=lingow-development \
    bash "$lock_release/deploy.sh" "$lock_deployment" "$lock_release" --no-smoke
  status=$?
  set -e
  exec 9>&-
  if [[ "$status" -ne 75 ]]; then
    printf 'expected deployment lock failure, got %s\n' "$status" >&2
    exit 1
  fi
  printf 'deployment lock prevents concurrent release\n'
else
  printf 'deployment lock test skipped: flock is unavailable\n'
fi

first_failed_deployment="$test_root/first-failed"
first_failed_release="$first_failed_deployment/.staging/candidate"
prepare_release "$first_failed_deployment" "$first_failed_release"
set +e
run_release "$first_failed_deployment" "$first_failed_release" 0
status=$?
set -e
if [[ "$status" -eq 0 ]]; then
  printf 'expected first deployment smoke failure\n' >&2
  exit 1
fi
grep -q -- ' down --remove-orphans' "$test_root/docker.log"
if grep -q -- ' down --remove-orphans --volumes' "$test_root/docker.log"; then
  printf 'first deployment removed data volumes\n' >&2
  exit 1
fi
printf 'first deployment failure removes containers without volumes\n'

rollback_first="$test_root/rollback-first"
mkdir -p "$rollback_first"
printf 'LINGOW_DEPLOY_ENV=development\n' > "$rollback_first/.env.production"
printf 'compose\n' > "$rollback_first/docker-compose.yml"
set +e
DEPLOY_EXPECTED_ENV=development DEPLOY_PROJECT_NAME=lingow-development \
  PATH="$test_root/bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
  bash "$repo_dir/scripts/rollback.sh" "$rollback_first"
status=$?
set -e
if [[ "$status" -ne 66 ]]; then
  printf 'expected first-release rollback refusal, got %s\n' "$status" >&2
  exit 1
fi
DEPLOY_EXPECTED_ENV=development DEPLOY_PROJECT_NAME=lingow-development ROLLBACK_CLEAN_FIRST_RELEASE=true \
  PATH="$test_root/bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
  bash "$repo_dir/scripts/rollback.sh" "$rollback_first"
grep -q -- ' down --remove-orphans' "$test_root/docker.log"
printf 'first-release rollback cleanup is explicit and volume-preserving\n'

observe_deployment="$test_root/observe"
observe_release="$observe_deployment/.staging/candidate"
prepare_release "$observe_deployment" "$observe_release"
: > "$test_root/metrics-count"
set +e
printf 'metrics-token\n' | FAKE_METRICS_FAIL=1 FAKE_METRICS_COUNT_FILE="$test_root/metrics-count" \
  PATH="$test_root/bin:$PATH" DEPLOY_OBSERVE_INTERVAL_SECONDS=1 \
  bash "$observe_release/observe.sh" "$observe_deployment" "$observe_release" 1
status=$?
set -e
if [[ "$status" -ne 1 ]]; then
  printf 'expected observation threshold failure, got %s\n' "$status" >&2
  exit 1
fi
printf 'deployment observation rejects metric failure increments\n'
