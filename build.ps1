param(
  [string]$Version = "v1.0.0",
  [switch]$Cli,
  [switch]$Gui
)
# EnvSwitch 本地构建脚本（Windows PowerShell）
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
$LdFlags = "-s -w -X github.com/MUHUIX/envswitch/internal/core.Version=$Version"

if (-not $Cli -and -not $Gui) { $Cli = $true; $Gui = $true }

New-Item -ItemType Directory -Force -Path build\bin | Out-Null

if ($Cli) {
  Write-Host "== Building envs.exe (CLI) =="
  go build -trimpath -ldflags $LdFlags -o build\bin\envs.exe .\cmd\envs
}
if ($Gui) {
  Write-Host "== Building frontend =="
  Push-Location frontend
  if (-not (Test-Path node_modules)) { npm install --no-fund --no-audit }
  npm run build
  Pop-Location
  Write-Host "== Building EnvSwitch.exe (Desktop GUI) =="
  go build -tags production -trimpath -ldflags "$LdFlags -H=windowsgui" -o build\bin\EnvSwitch.exe .
}
Write-Host "== Build Complete =="
Get-ChildItem build\bin\*.exe | ForEach-Object { "{0}  {1:N1} MB" -f $_.Name, ($_.Length/1MB) }
