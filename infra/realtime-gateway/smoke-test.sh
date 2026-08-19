#!/bin/sh

set -eu

gateway_url="${LINGOW_REALTIME_GATEWAY_URL:-http://127.0.0.1:8090}"

route_for() {
  path="$1"
  headers="$(mktemp)"

  if ! http_status="$(curl --silent --show-error \
    --output /dev/null \
    --dump-header "$headers" \
    --write-out '%{http_code}' \
    --header "X-Lingow-Route-Debug: 1" \
    "${gateway_url}${path}")"; then
    rm -f "$headers"
    return 1
  fi

  case "$http_status" in
    2??|3??|4??) ;;
    *)
      echo "gateway returned HTTP ${http_status} for ${path}" >&2
      rm -f "$headers"
      return 1
      ;;
  esac

  awk -F ': ' 'tolower($1) == "x-lingow-realtime-upstream" { gsub("\r", "", $2); print $2 }' "$headers" \
    | sed 's/.*://'
  rm -f "$headers"
}

assert_session_is_sticky() {
  session_id="sticky-session"
  expected=""

  for suffix in runtime mode connection webrtc/config; do
    actual="$(route_for "/realtime/v1/sessions/${session_id}/${suffix}")"
    if [ -z "$actual" ]; then
      echo "gateway did not report an upstream for ${suffix}" >&2
      exit 1
    fi
    if [ -z "$expected" ]; then
      expected="$actual"
    elif [ "$actual" != "$expected" ]; then
      echo "session moved from port ${expected} to ${actual}" >&2
      exit 1
    fi
  done

  echo "sticky routing: session stayed on realtime port ${expected}"
}

assert_sessions_are_distributed() {
  saw_8091=false
  saw_8092=false
  index=1

  while [ "$index" -le 64 ]; do
    port="$(route_for "/realtime/v1/sessions/random-session-${index}/runtime")"
    case "$port" in
      8091) saw_8091=true ;;
      8092) saw_8092=true ;;
      *)
        echo "unexpected realtime upstream port: ${port:-missing}" >&2
        exit 1
        ;;
    esac
    index=$((index + 1))
  done

  if [ "$saw_8091" != true ] || [ "$saw_8092" != true ]; then
    echo "random sessions were not distributed across both realtime nodes" >&2
    exit 1
  fi

  echo "distribution: random sessions reached realtime ports 8091 and 8092"
}

assert_session_is_sticky
assert_sessions_are_distributed
