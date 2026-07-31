# One-click local Lingow stack: API (:8080) + realtime-audio (:8090).
# Loads root .env into the process environment (go run does NOT read .env itself).
#
# Default: start both (realtime in a child window, API in this window).
# Optional:
#   .\start-local.ps1 -UseDocker          also start infra/docker-compose.yml
#   .\start-local.ps1 -Service api        API only
#   .\start-local.ps1 -Service realtime   realtime only

param(
  [switch]$UseDocker,
  [ValidateSet("all", "api", "realtime")]
  [string]$Service = "all"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

function Import-DotEnv {
  param([Parameter(Mandatory = $true)][string]$Path)

  if (-not (Test-Path $Path)) {
    throw "Missing $Path — copy .env.example to .env first."
  }

  Get-Content -LiteralPath $Path | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) {
      return
    }
    $eq = $line.IndexOf("=")
    if ($eq -lt 1) {
      return
    }
    $key = $line.Substring(0, $eq).Trim()
    $value = $line.Substring($eq + 1).Trim()
    if (
      ($value.StartsWith('"') -and $value.EndsWith('"')) -or
      ($value.StartsWith("'") -and $value.EndsWith("'"))
    ) {
      $value = $value.Substring(1, $value.Length - 2)
    }
    if ($key) {
      Set-Item -Path "Env:$key" -Value $value
    }
  }
}

function Invoke-Compose {
  param([Parameter(Mandatory = $true)][string[]]$ComposeArgs)
  $prev = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    & docker compose @ComposeArgs
    return $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $prev
  }
}

function Wait-PostgresReady {
  param(
    [Parameter(Mandatory = $true)][string]$ComposeFile,
    [int]$TimeoutSeconds = 60
  )

  Write-Host "==> Waiting for Docker Postgres..."
  for ($i = 0; $i -lt $TimeoutSeconds; $i++) {
    $code = Invoke-Compose -ComposeArgs @(
      "-f", $ComposeFile, "exec", "-T", "postgres",
      "pg_isready", "-U", "postgres", "-h", "localhost"
    )
    if ($code -eq 0) {
      Write-Host "    Docker Postgres is ready."
      return
    }
    Start-Sleep -Seconds 1
  }
  throw "Docker Postgres did not become ready within ${TimeoutSeconds}s"
}

function Assert-ApiEnv {
  foreach ($key in @("DATABASE_URL", "JWT_SECRET")) {
    $value = [Environment]::GetEnvironmentVariable($key, "Process")
    if ([string]::IsNullOrWhiteSpace($value)) {
      throw "$key is empty after loading .env"
    }
  }
  Write-Host "    DATABASE_URL host: $($env:DATABASE_URL -replace '://([^:]+:)?[^@]+@', '://***@')"
  if ($env:REDIS_URL) {
    Write-Host "    REDIS_URL:         $($env:REDIS_URL)"
  }
  if ($env:LINGOW_SESSION_RUNTIME -eq "enabled") {
    if (-not $env:REALTIME_BASE_URL) {
      throw "LINGOW_SESSION_RUNTIME=enabled requires REALTIME_BASE_URL"
    }
    if (-not $env:REALTIME_TICKET_SECRET -or $env:REALTIME_TICKET_SECRET.Length -lt 32) {
      throw "LINGOW_SESSION_RUNTIME=enabled requires REALTIME_TICKET_SECRET (>= 32 bytes)"
    }
    Write-Host "    Session runtime: enabled"
    Write-Host "    Realtime base:   $($env:REALTIME_BASE_URL)"
  } else {
    Write-Host "    Session runtime: $($env:LINGOW_SESSION_RUNTIME)"
    Write-Host "    WARNING: voice-sessions will return 501 until LINGOW_SESSION_RUNTIME=enabled"
  }
}

function Assert-RealtimeEnv {
  if ([string]::IsNullOrWhiteSpace($env:REALTIME_ADDR)) {
    $env:REALTIME_ADDR = ":8090"
  }
  $secret = [Environment]::GetEnvironmentVariable("REALTIME_TICKET_SECRET", "Process")
  if ([string]::IsNullOrWhiteSpace($secret) -or $secret.Length -lt 32) {
    throw "REALTIME_TICKET_SECRET must be set in .env (>= 32 bytes)"
  }
  Write-Host "    REALTIME_ADDR=$($env:REALTIME_ADDR)"
}

function Start-RealtimeProcess {
  Write-Host "==> Starting realtime-audio in a child window..."
  $ps1 = Join-Path $Root "start-local.ps1"
  Start-Process -FilePath "powershell" -ArgumentList @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", $ps1,
    "-Service", "realtime"
  ) -WorkingDirectory $Root
}

function Invoke-GoRun {
  param([Parameter(Mandatory = $true)][string]$ServiceDir)
  Set-Location (Join-Path $Root $ServiceDir)
  $prev = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    & go run .
    exit $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $prev
  }
}

Write-Host "==> Loading .env into process environment..."
Import-DotEnv -Path (Join-Path $Root ".env")

switch ($Service) {
  "realtime" {
    Assert-RealtimeEnv
    Write-Host "    Providers: ASR=$($env:ASR_PROVIDER) LLM=$($env:LLM_PROVIDER) TTS=$($env:TTS_PROVIDER)"
    $srcLang = if ($env:REALTIME_SOURCE_LANGUAGE) { $env:REALTIME_SOURCE_LANGUAGE } else { "zh-CN" }
    $tgtLang = if ($env:REALTIME_TARGET_LANGUAGE) { $env:REALTIME_TARGET_LANGUAGE } else { "en-US" }
    Write-Host "    Languages: $srcLang → $tgtLang"
    if ($env:REALTIME_TTS_DOWNLINK) {
      Write-Host "    TTS downlink: $($env:REALTIME_TTS_DOWNLINK)"
    } else {
      Write-Host "    TTS downlink: none (subtitles only; TTS forced mock)"
    }
    if ($env:REALTIME_TTS_DOWNLINK -eq "pcm") {
      Write-Host "    TTS audio: DataChannel PCM → browser Web Audio"
    }
    Write-Host "==> Starting realtime-audio control-plane..."
    Write-Host "    Keep this window open. Ctrl+C to stop."
    Write-Host ""
    Invoke-GoRun -ServiceDir "services\realtime-audio"
  }
  "api" {
    Assert-ApiEnv
    if ($UseDocker) {
      $composeFile = Join-Path $Root "infra\docker-compose.yml"
      Write-Host "==> -UseDocker: starting Postgres / Redis via docker compose..."
      $upCode = Invoke-Compose -ComposeArgs @("-f", $composeFile, "up", "-d")
      if ($upCode -ne 0) {
        throw "docker compose failed (is Docker Desktop running?)"
      }
      Wait-PostgresReady -ComposeFile $composeFile
    } else {
      Write-Host "==> Using your local Postgres/Redis from .env (Docker infra skipped)."
    }
    Write-Host "==> Starting API on $($env:API_ADDR)"
    Write-Host "    Keep this window open. Ctrl+C to stop."
    Write-Host ""
    Invoke-GoRun -ServiceDir "services\api"
  }
  default {
    Assert-ApiEnv
    Assert-RealtimeEnv
    if ($UseDocker) {
      $composeFile = Join-Path $Root "infra\docker-compose.yml"
      Write-Host "==> -UseDocker: starting Postgres / Redis via docker compose..."
      $upCode = Invoke-Compose -ComposeArgs @("-f", $composeFile, "up", "-d")
      if ($upCode -ne 0) {
        throw "docker compose failed (is Docker Desktop running?)"
      }
      Wait-PostgresReady -ComposeFile $composeFile
    } else {
      Write-Host "==> Using your local Postgres/Redis from .env (Docker infra skipped)."
    }
    Start-RealtimeProcess
    Start-Sleep -Seconds 1
    Write-Host "==> Starting API on $($env:API_ADDR)"
    Write-Host "    Realtime runs in a separate window; Ctrl+C here stops API only."
    Write-Host ""
    Invoke-GoRun -ServiceDir "services\api"
  }
}
