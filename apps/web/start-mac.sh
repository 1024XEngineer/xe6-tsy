#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
WEB_PID_FILE="$ROOT/.web.pid"
LOG_DIR="$ROOT/logs"
mkdir -p "$LOG_DIR"

if [[ ! -f "$ROOT/.env.local" ]]; then
  echo "缺少 .env.local，请先: cp .env.example .env.local 并填入 REALTIME_TICKET_SECRET"
fi

if [[ -f "$WEB_PID_FILE" ]] && kill -0 "$(cat "$WEB_PID_FILE")" 2>/dev/null; then
  echo "前端已在运行 (PID $(cat "$WEB_PID_FILE"))"
else
  echo "==> 启动 Lingow 联调前端 (端口 3000)..."
  echo "    请确保 xe6-tsy API (:8080) 已运行；realtime (:8090) 就绪后再测完整 WebRTC。"
  cd "$ROOT"
  if [[ ! -d node_modules ]]; then
    npm install
  fi
  nohup npm run dev >"$LOG_DIR/web.log" 2>&1 &
  echo $! >"$WEB_PID_FILE"
  sleep 4
  if curl -sf http://127.0.0.1:3000 >/dev/null; then
    echo "    前端就绪 ✓"
  else
    echo "    前端启动中，请稍候查看 $LOG_DIR/web.log"
  fi
fi

echo ""
echo "=========================================="
echo "  Lingow 联调前端已启动"
echo "  浏览器打开: http://localhost:3000"
echo "  停止服务: ./stop-mac.sh"
echo "=========================================="
echo ""

if command -v open >/dev/null 2>&1; then
  open "http://localhost:3000"
fi
