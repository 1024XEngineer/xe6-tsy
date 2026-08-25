#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file> <session-id>\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
environment_file=$2
session_id=$3
access_token=$(cat)

[[ "$session_id" =~ ^[A-Za-z0-9._:-]+$ ]] || { printf 'invalid session ID\n' >&2; exit 64; }
[[ -n "$access_token" ]] || { printf 'missing smoke access token\n' >&2; exit 64; }

compose=(docker compose --project-name lingow --env-file "$environment_file" --file "$deployment_dir/docker-compose.yml")
"${compose[@]}" exec -T api curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
"${compose[@]}" exec -T realtime-audio curl --fail --silent http://127.0.0.1:8090/healthz >/dev/null

ticket_response=$("${compose[@]}" exec -T api curl --fail --silent --show-error \
  --header "Authorization: Bearer ${access_token}" \
  --request POST "http://127.0.0.1:8080/api/v1/voice-sessions/${session_id}/realtime-ticket")
ticket=$(printf '%s' "$ticket_response" | sed -n 's/.*"ticket":"\([^"]*\)".*/\1/p')
[[ -n "$ticket" ]] || { printf 'realtime ticket response did not contain a ticket\n' >&2; exit 1; }

config_response=$("${compose[@]}" exec -T realtime-audio curl --fail --silent --show-error \
  --header "Authorization: Bearer ${ticket}" \
  "http://127.0.0.1:8090/realtime/v1/sessions/${session_id}/webrtc/config")
printf '%s' "$config_response" | grep -q '"session_id":"'"$session_id"'"' || { printf 'WebRTC config session mismatch\n' >&2; exit 1; }
printf '%s' "$config_response" | grep -q '"ice_servers"' || { printf 'WebRTC config omitted ICE servers\n' >&2; exit 1; }
printf '%s' "$config_response" | grep -Eq '"urls":\[[^]]*"turns?:' || { printf 'WebRTC config omitted TURN server\n' >&2; exit 1; }

printf 'deployment smoke checks passed\n'
