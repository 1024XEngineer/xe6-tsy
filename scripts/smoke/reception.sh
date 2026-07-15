#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "缺少 curl"
command -v jq >/dev/null 2>&1 || fail "缺少 jq，请安装后重试"

curl -fsS "${BASE_URL}/healthz" | jq -e '.status == "ok"' >/dev/null || fail "healthz 检查失败"

create_session() {
  local key="$1"
  curl -fsS -X POST "${BASE_URL}/api/v1/reception/sessions" \
    -H 'Content-Type: application/json' \
    -d "{\"idempotency_key\":\"${key}\",\"access_context_ref\":\"access-demo\",\"organization_id\":\"trial-org\",\"service_point_id\":\"service-point-001\",\"service_window_id\":\"window-001\",\"organization_config_version\":\"config-v1\",\"processing_context_ref\":\"processing-demo\"}"
}

start_session() {
  local session_id="$1" version="$2" key="$3"
  curl -fsS -X POST "${BASE_URL}/api/v1/reception/sessions/${session_id}/start" \
    -H 'Content-Type: application/json' \
    -d "{\"access_context_ref\":\"access-demo\",\"expected_version\":${version},\"idempotency_key\":\"${key}\"}"
}

created="$(create_session smoke-create-success)"
session_id="$(jq -er '.data.session_id' <<<"${created}")"
version="$(jq -er '.data.version' <<<"${created}")"
started="$(start_session "${session_id}" "${version}" smoke-start-success)"
version="$(jq -er '.data.session.version' <<<"${started}")"

attached="$(curl -fsS -X POST "${BASE_URL}/api/v1/reception/sessions/${session_id}/media-tracks" \
  -H 'Content-Type: application/json' \
  -d "{\"access_context_ref\":\"access-demo\",\"expected_session_version\":${version},\"idempotency_key\":\"smoke-attach-success\",\"track_ref\":\"smoke-track-success\",\"scenario\":\"success\"}")"
binding_id="$(jq -er '.data.binding.binding_id' <<<"${attached}")"
binding_version="$(jq -er '.data.binding.version' <<<"${attached}")"

curl -fsS -X POST "${BASE_URL}/api/v1/reception/sessions/${session_id}/media-tracks/${binding_id}/detach" \
  -H 'Content-Type: application/json' \
  -d "{\"access_context_ref\":\"access-demo\",\"expected_binding_version\":${binding_version},\"idempotency_key\":\"smoke-detach-success\"}" \
  | jq -e '.data.status == "detached"' >/dev/null || fail "媒体断开失败"

curl -fsS -X POST "${BASE_URL}/api/v1/reception/sessions/${session_id}/end" \
  -H 'Content-Type: application/json' \
  -d "{\"access_context_ref\":\"access-demo\",\"expected_version\":${version},\"idempotency_key\":\"smoke-end-success\"}" \
  | jq -e '.data.status == "ended"' >/dev/null || fail "会话结束失败"

failed_created="$(create_session smoke-create-failure)"
failed_session_id="$(jq -er '.data.session_id' <<<"${failed_created}")"
failed_version="$(jq -er '.data.version' <<<"${failed_created}")"
failed_started="$(start_session "${failed_session_id}" "${failed_version}" smoke-start-failure)"
failed_version="$(jq -er '.data.session.version' <<<"${failed_started}")"

failure_body="$(mktemp)"
trap 'rm -f "${failure_body}"' EXIT
status="$(curl -sS -o "${failure_body}" -w '%{http_code}' -X POST "${BASE_URL}/api/v1/reception/sessions/${failed_session_id}/media-tracks" \
  -H 'Content-Type: application/json' \
  -d "{\"access_context_ref\":\"access-demo\",\"expected_session_version\":${failed_version},\"idempotency_key\":\"smoke-attach-failure\",\"track_ref\":\"smoke-track-failure\",\"scenario\":\"attach_failure\"}")"
[[ "${status}" == "503" ]] || fail "attach_failure 应返回 503，实际 ${status}"
jq -e '.data.binding.status == "failed" and .degradation.mode == "manual_text" and .degradation.session_remains_active == true' "${failure_body}" >/dev/null || fail "attach_failure 降级结构不正确"

curl -fsS "${BASE_URL}/api/v1/reception/sessions/${failed_session_id}" \
  | jq -e '.data.status == "active"' >/dev/null || fail "attach_failure 后会话未保持 active"

echo "PASS: reception success lifecycle and attach_failure degradation"
