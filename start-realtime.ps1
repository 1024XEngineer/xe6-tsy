# One-click local realtime-audio control-plane startup.
# Loads root .env into the process environment (go run does NOT read .env itself).

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
    if (-not $line -or $line.StartsWith("#")) { return }
    $eq = $line.IndexOf("=")
    if ($eq -lt 1) { return }
    $key = $line.Substring(0, $eq).Trim()
    $value = $line.Substring($eq + 1).Trim()
    if (
      ($value.StartsWith('"') -and $value.EndsWith('"')) -or
      ($value.StartsWith("'") -and $value.EndsWith("'"))
    ) {
      $value = $value.Substring(1, $value.Length - 2)
    }
    if ($key) { Set-Item -Path "Env:$key" -Value $value }
  }
}

Write-Host "==> Loading .env into process environment..."
Import-DotEnv -Path (Join-Path $Root ".env")

if ([string]::IsNullOrWhiteSpace($env:REALTIME_ADDR)) {
  $env:REALTIME_ADDR = ":8090"
}
$secret = [Environment]::GetEnvironmentVariable("REALTIME_TICKET_SECRET", "Process")
if ([string]::IsNullOrWhiteSpace($secret) -or $secret.Length -lt 32) {
  throw "REALTIME_TICKET_SECRET must be set in .env (>= 32 bytes)"
}

Write-Host "    REALTIME_ADDR=$($env:REALTIME_ADDR)"
Write-Host "==> Starting realtime-audio control-plane..."
Write-Host "    Keep this window open. Ctrl+C to stop."
Write-Host ""

Set-Location (Join-Path $Root "services\realtime-audio")
$prev = $ErrorActionPreference
$ErrorActionPreference = "Continue"
try {
  & go run .
  exit $LASTEXITCODE
} finally {
  $ErrorActionPreference = $prev
}
