#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 && $# -ne 3 ]]; then
  printf 'usage: %s <deployment-directory> <environment-file> [session-id]\n' "$0" >&2
  exit 64
fi

deployment_dir=$1
environment_file=$2
session_id=${3:-}
access_token=$(cat | tr -d '\r\n')

project_name="${DEPLOY_PROJECT_NAME:-lingow}"
if [[ ! "$project_name" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  printf 'invalid DEPLOY_PROJECT_NAME\n' >&2
  exit 64
fi

compose=(docker compose --project-name "$project_name" --env-file "$environment_file" --file "$deployment_dir/docker-compose.yml")
"${compose[@]}" exec -T api curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
"${compose[@]}" exec -T realtime-audio curl --fail --silent http://127.0.0.1:8090/healthz >/dev/null

created_session=false
refresh_token=''
cleanup() {
  if [[ "$created_session" != true || -z "$session_id" || -z "$access_token" ]]; then
    return 0
  fi
  "${compose[@]}" exec -T api curl --fail --silent --show-error \
    --header "Authorization: Bearer ${access_token}" \
    --header "Idempotency-Key: deploy-smoke-end-${session_id}" \
    --request POST "http://127.0.0.1:8080/api/v1/voice-sessions/${session_id}/end" >/dev/null || true
  if [[ -n "$refresh_token" ]]; then
    "${compose[@]}" exec -T api curl --fail --silent --show-error \
      --header 'Content-Type: application/json' \
      --data "{\"refresh_token\":\"${refresh_token}\"}" \
      --request POST http://127.0.0.1:8080/api/v1/auth/logout >/dev/null || true
  fi
}
trap cleanup EXIT

# A persistent smoke token is prone to expiring between deployments. When the
# legacy token/session pair is absent, create an isolated anonymous account and
# session so every release validates the current authentication path.
if [[ -z "$access_token" || -z "$session_id" ]]; then
  auth_response=$("${compose[@]}" exec -T api curl --fail --silent --show-error \
    --request POST http://127.0.0.1:8080/api/v1/auth/anonymous)
  access_token=$(printf '%s' "$auth_response" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
  refresh_token=$(printf '%s' "$auth_response" | sed -n 's/.*"refresh_token":"\([^"]*\)".*/\1/p')
  [[ -n "$access_token" ]] || { printf 'anonymous auth response did not contain an access token\n' >&2; exit 1; }
  session_response=$("${compose[@]}" exec -T api curl --fail --silent --show-error \
    --header "Authorization: Bearer ${access_token}" \
    --header "Idempotency-Key: deploy-smoke-create-$(date +%s%N)" \
    --header 'Content-Type: application/json' \
    --data '{"audio_config":{"codec":"opus","sample_rate_hz":48000,"channels":1,"echo_cancellation":true,"noise_suppression":true,"auto_gain_control":true},"capabilities":{"webrtc":true,"data_channel":true,"microphone":true,"speaker":true,"speaker_diarization":true}}' \
    --request POST http://127.0.0.1:8080/api/v1/voice-sessions)
  session_id=$(printf '%s' "$session_response" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  [[ -n "$session_id" ]] || { printf 'voice session response did not contain an ID\n' >&2; exit 1; }
  created_session=true
fi

[[ "$session_id" =~ ^[A-Za-z0-9._:-]+$ ]] || { printf 'invalid session ID\n' >&2; exit 64; }

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
