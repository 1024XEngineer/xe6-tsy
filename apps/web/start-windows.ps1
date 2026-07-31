$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$LogDir = Join-Path $Root "logs"
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

if (-not (Test-Path (Join-Path $Root ".env.local"))) {
  Write-Host "缺少 .env.local，请先: copy .env.example .env.local 并填入 REALTIME_TICKET_SECRET"
}

Write-Host "==> 启动 Lingow 联调前端 (端口 3000)..."
Write-Host "    请确保 xe6-tsy API (:8080) 已运行；realtime (:8090) 就绪后再测完整 WebRTC。"
Set-Location $Root
if (-not (Test-Path "node_modules")) {
  npm install
}
Start-Process -WindowStyle Hidden -FilePath "npm" -ArgumentList "run dev" -RedirectStandardOutput (Join-Path $LogDir "web.log") -RedirectStandardError (Join-Path $LogDir "web.err.log")

Start-Sleep -Seconds 5
Start-Process "http://localhost:3000"

Write-Host ""
Write-Host "Lingow 联调前端已启动"
Write-Host "浏览器: http://localhost:3000"
Write-Host "日志: $LogDir\web.log"
