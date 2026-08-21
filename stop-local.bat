@echo off
setlocal EnableExtensions
cd /d "%~dp0"

REM Escape ">" so cmd does not treat "==>" as a redirect into a file named Stopping.
echo ==^> Local stop helper
echo.
echo Note: this does not kill running go run windows.
echo Close the API / realtime consoles or press Ctrl+C there.
echo Local Postgres/Redis are left running ^(start/stop them yourself^).
echo.

where docker >nul 2>nul
if errorlevel 1 (
  echo Docker CLI not found — skipped compose stop.
  goto :done
)

REM Do not wake Docker Desktop / WSL just to stop compose.
docker info >nul 2>nul
if errorlevel 1 (
  echo Docker daemon is not running — skipped compose stop.
  goto :done
)

echo ==^> Docker is already running; stopping infra containers if any...
docker compose -f "%~dp0infra\docker-compose.yml" stop
if errorlevel 1 (
  echo compose stop reported an error ^(containers may already be stopped^).
)

:done
echo.
pause
