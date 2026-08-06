@echo off
setlocal EnableExtensions
cd /d "%~dp0"

echo ==> Stopping infra containers (if any)...
docker compose -f "%~dp0infra\docker-compose.yml" stop
echo.
echo Note: this does not kill running go run windows.
echo Close the API / realtime consoles or press Ctrl+C there.
pause
