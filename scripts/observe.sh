#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  printf 'usage: %s <deployment-directory> <release-directory> [duration-seconds]\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
release_dir=$2
duration_seconds=${3:-${DEPLOY_OBSERVE_SECONDS:-0}}
project_name="${DEPLOY_PROJECT_NAME:-lingow}"
interval_seconds="${DEPLOY_OBSERVE_INTERVAL_SECONDS:-30}"
max_provider_failures="${DEPLOY_OBSERVE_MAX_PROVIDER_FAILURES:-0}"
max_data_channel_failures="${DEPLOY_OBSERVE_MAX_DATA_CHANNEL_FAILURES:-0}"
max_interpretation_failures="${DEPLOY_OBSERVE_MAX_INTERPRETATION_FAILURES:-0}"

for value_name in duration_seconds interval_seconds max_provider_failures max_data_channel_failures max_interpretation_failures; do
  value=${!value_name}
  if [[ ! "$value" =~ ^[0-9]+$ ]]; then
    printf '%s must be a non-negative integer\n' "$value_name" >&2
    exit 64
  fi
done
if [[ ! "$project_name" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  printf 'invalid DEPLOY_PROJECT_NAME\n' >&2
  exit 64
fi
case "$release_dir" in
  "$deployment_dir"/.staging/*) ;;
  *) printf 'release directory must be inside deployment .staging\n' >&2; exit 64 ;;
esac
if (( duration_seconds == 0 )); then
  printf 'deployment observation skipped (duration is zero)\n'
  exit 0
fi
if (( interval_seconds == 0 )); then
  printf 'interval must be greater than zero when observation is enabled\n' >&2
  exit 64
fi

metrics_token=$(cat)
if [[ -z "$metrics_token" ]]; then
  printf 'missing realtime metrics token\n' >&2
  exit 64
fi

compose=(docker compose --project-name "$project_name" --env-file "$release_dir/.env.production" --file "$release_dir/docker-compose.yml")
read_metrics() {
  "${compose[@]}" exec -T realtime-audio curl --fail --silent --show-error \
    --header "Authorization: Bearer ${metrics_token}" http://127.0.0.1:8090/metrics
}
json_counter() {
  local json=$1
  local field=$2
  printf '%s' "$json" | sed -n "s/.*\"${field}\":\([0-9][0-9]*\).*/\1/p" | head -1
}
provider_failures() {
  local json=$1
  local total=0 value
  for field in asr assistant translation tts; do
    value=$(json_counter "$json" "$field")
    [[ "$value" =~ ^[0-9]+$ ]] || { printf 'metrics response omitted provider counter %s\n' "$field" >&2; return 1; }
    total=$((total + value))
  done
  printf '%s\n' "$total"
}
interpretation_failures() {
  local value
  value=$(json_counter "$1" interpretation_failures)
  [[ "$value" =~ ^[0-9]+$ ]] || { printf 'metrics response omitted interpretation_failures counter\n' >&2; return 1; }
  printf '%s\n' "$value"
}

baseline=$(read_metrics)
baseline_provider=$(provider_failures "$baseline")
baseline_data_channel=$(json_counter "$baseline" data_channel_failures)
[[ "$baseline_data_channel" =~ ^[0-9]+$ ]] || { printf 'metrics response omitted data_channel_failures counter\n' >&2; exit 1; }
baseline_interpretation=$(interpretation_failures "$baseline")
deadline=$((SECONDS + duration_seconds))
while (( SECONDS < deadline )); do
  remaining=$((deadline - SECONDS))
  sleep_for=$interval_seconds
  if (( sleep_for > remaining )); then sleep_for=$remaining; fi
  sleep "$sleep_for"
  current=$(read_metrics)
  current_provider=$(provider_failures "$current")
  current_data_channel=$(json_counter "$current" data_channel_failures)
  [[ "$current_data_channel" =~ ^[0-9]+$ ]] || { printf 'metrics response omitted data_channel_failures counter\n' >&2; exit 1; }
  current_interpretation=$(interpretation_failures "$current")
  provider_delta=$((current_provider - baseline_provider))
  data_channel_delta=$((current_data_channel - ${baseline_data_channel:-0}))
  interpretation_delta=$((current_interpretation - ${baseline_interpretation:-0}))
  printf 'observation counters: provider_failures=%s data_channel_failures=%s interpretation_failures=%s\n' \
    "$provider_delta" "$data_channel_delta" "$interpretation_delta"
  if (( provider_delta < 0 || data_channel_delta < 0 || interpretation_delta < 0 )); then
    printf 'deployment observation counters reset; realtime process restarted\n' >&2
    exit 1
  fi
  if (( provider_delta > max_provider_failures || data_channel_delta > max_data_channel_failures || interpretation_delta > max_interpretation_failures )); then
    printf 'deployment observation threshold exceeded\n' >&2
    exit 1
  fi
done
printf 'deployment observation passed (%ss)\n' "$duration_seconds"
