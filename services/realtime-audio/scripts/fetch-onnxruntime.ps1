# Download ONNX Runtime 1.24.1 for local Silero VAD (Windows amd64).
param(
  [string]$Version = "1.24.1",
  [string]$DestRoot = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = "Stop"
$ortDir = Join-Path $DestRoot "third_party\onnxruntime"
$libDll = Join-Path $ortDir "lib\onnxruntime.dll"
if (Test-Path $libDll) {
  Write-Host "Already present: $libDll"
  exit 0
}

New-Item -ItemType Directory -Force -Path $ortDir | Out-Null
$zip = Join-Path $ortDir "ort.zip"
$url = "https://github.com/microsoft/onnxruntime/releases/download/v$Version/onnxruntime-win-x64-$Version.zip"
Write-Host "Downloading $url ..."
Invoke-WebRequest -Uri $url -OutFile $zip
$extract = Join-Path $ortDir "extract"
Expand-Archive -Path $zip -DestinationPath $extract -Force
$inner = Get-ChildItem $extract -Directory | Select-Object -First 1
if (-not $inner) {
  throw "onnxruntime archive layout unexpected"
}
Copy-Item -Recurse -Force (Join-Path $inner.FullName "*") $ortDir
Remove-Item -Recurse -Force $extract, $zip
if (-not (Test-Path $libDll)) {
  throw "download finished but $libDll is missing"
}
Write-Host "Installed $libDll"
