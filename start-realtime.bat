@echo off
setlocal EnableExtensions
cd /d "%~dp0"

echo ==========================================
echo   Lingow realtime-audio one-click start
echo   Loads .env then: go run . on :8090
echo ==========================================
echo.

where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] go not found. Install Go and ensure it is on PATH.
  pause
  exit /b 1
)

if not exist "%~dp0.env" (
  echo [ERROR] .env not found. Copy .env.example to .env first.
  pause
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start-realtime.ps1" %*
set "ERR=%ERRORLEVEL%"
echo.
if not "%ERR%"=="0" (
  echo [ERROR] realtime-audio exited with code %ERR%
  pause
  exit /b %ERR%
)
pause
exit /b 0
