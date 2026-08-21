#!/usr/bin/env bash

set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

deployment_dir="$test_root/deployment"
release_dir="$deployment_dir/.staging/candidate"
mkdir -p "$release_dir" "$test_root/bin"

printf 'old\n' > "$deployment_dir/.env.production"
printf 'old compose\n' > "$deployment_dir/docker-compose.yml"
cp "$repo_dir/scripts/deploy.sh" "$deployment_dir/deploy.sh"
cp "$repo_dir/scripts/deploy-smoke.sh" "$deployment_dir/deploy-smoke.sh"
printf 'new\n' > "$release_dir/.env.production"
printf 'new compose\n' > "$release_dir/docker-compose.yml"
cp "$repo_dir/scripts/deploy.sh" "$release_dir/deploy.sh"
cp "$repo_dir/scripts/deploy-smoke.sh" "$release_dir/deploy-smoke.sh"

printf '%s\n' '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'case "$*" in' \
  '  *realtime-ticket*) printf '\''{"ticket":"ticket"}'\'' ;;' \
  '  *webrtc/config*) printf '\''{"session_id":"session","ice_servers":[{"urls":["stun:stun.example"]}]}'\'' ;;' \
  'esac' \
  > "$test_root/bin/docker"
chmod 700 "$test_root/bin/docker"

set +e
printf 'smoke-token\n' | PATH="$test_root/bin:$PATH" bash "$repo_dir/scripts/deploy.sh" "$deployment_dir" "$release_dir" session
status=$?
set -e

if [[ $status -eq 0 ]]; then
  printf 'expected smoke failure to fail deployment\n' >&2
  exit 1
fi
cmp "$deployment_dir/.env.production" <(printf 'old\n')
cmp "$deployment_dir/docker-compose.yml" <(printf 'old compose\n')
printf 'deployment smoke failure restores previous release\n'
