@echo off
setlocal EnableExtensions
cd /d "%~dp0"

echo ==========================================
echo   Lingow local one-click start
echo   API (:8080) + realtime-audio (:8090)
echo   Prefers LOCAL Postgres/Redis from .env
echo   Docker only if unreachable (prompt) or -UseDocker
echo ==========================================
echo.

where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] go not found. Install Go and ensure it is on PATH.
  pause
  exit /b 1
)

if not exist "%~dp0.env" (
  echo [ERROR] .env not found.
  echo         Copy .env.example to .env and fill secrets first.
  pause
  exit /b 1
)

REM Default: both services, local infra. Optional: start-local.bat -UseDocker
REM Single service: powershell -File start-local.ps1 -Service api|realtime
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-local.ps1" %*
set "ERR=%ERRORLEVEL%"
echo.
if not "%ERR%"=="0" (
  echo [ERROR] local stack exited with code %ERR%
  pause
  exit /b %ERR%
)
pause
exit /b 0
