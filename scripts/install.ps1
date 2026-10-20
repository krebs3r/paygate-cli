<#
.SYNOPSIS
  Installs the Paygate CLI from this source folder on Windows.

.DESCRIPTION
  Builds paygate.exe, copies it to %LOCALAPPDATA%\Programs\paygate and adds
  that folder to your user PATH. Run it from PowerShell:

    powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
    powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1 -Uninstall
#>
param([switch]$Uninstall)

$ErrorActionPreference = 'Stop'
$Root   = Split-Path -Parent $PSScriptRoot
$BinDir = Join-Path $env:LOCALAPPDATA 'Programs\paygate'
$Exe    = Join-Path $BinDir 'paygate.exe'

function Write-Ok([string]$Message) {
    Write-Host '  OK  ' -ForegroundColor Green -NoNewline
    Write-Host $Message
}

function Stop-WithError([string]$Message) {
    Write-Host ''
    Write-Host "  ERROR  $Message" -ForegroundColor Red
    Write-Host ''
    exit 1
}

function Get-UserPathEntries {
    $value = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $value) { return @() }
    return @($value -split ';' | Where-Object { $_ -ne '' })
}

function Set-UserPathEntries([string[]]$Entries) {
    [Environment]::SetEnvironmentVariable('Path', ($Entries -join ';'), 'User')
}

function Test-SameFolder([string]$A, [string]$B) {
    return $A.TrimEnd('\') -ieq $B.TrimEnd('\')
}

Write-Host ''

if ($Uninstall) {
    if (Test-Path $Exe) { Remove-Item $Exe -Force }
    if (Test-Path $BinDir) { Remove-Item $BinDir -Recurse -Force -ErrorAction SilentlyContinue }
    Write-Ok "Removed $BinDir"
    $kept = @(Get-UserPathEntries | Where-Object { -not (Test-SameFolder $_ $BinDir) })
    Set-UserPathEntries $kept
    Write-Ok 'Removed the PATH entry'
    Write-Host ''
    Write-Host '  Profiles stay in %APPDATA%\paygate-cli; API keys stay in the Credential Manager.' -ForegroundColor DarkGray
    Write-Host ''
    exit 0
}

Write-Host '  Installing the Paygate CLI' -ForegroundColor White
Write-Host ''

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Stop-WithError 'Go is missing. Install it with:  winget install --id GoLang.Go -e   then open a NEW PowerShell window and run this script again.'
}
$goVersion = ((& go version) -split ' ')[2]
Write-Ok "Go found ($goVersion)"

$Version = (Get-Content (Join-Path $Root 'VERSION') -Raw).Trim()
$Commit = 'unknown'
if (Get-Command git -ErrorAction SilentlyContinue) {
    $rev = & git -C $Root rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0 -and $rev) { $Commit = $rev.Trim() }
}
$Date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Write-Host '  ...   building (the first build downloads dependencies and takes a minute)' -ForegroundColor DarkGray
Push-Location $Root
try {
    & go build -ldflags "-s -w -X main.version=$Version -X main.commit=$Commit -X main.date=$Date" -o $Exe ./cmd/paygate
    $buildExit = $LASTEXITCODE
} finally {
    Pop-Location
}
if ($buildExit -ne 0) { Stop-WithError 'The build failed. See the error above.' }
Write-Ok "Installed paygate $Version to $Exe"

$entries = Get-UserPathEntries
if (@($entries | Where-Object { Test-SameFolder $_ $BinDir }).Count -gt 0) {
    Write-Ok "$BinDir is already on your PATH"
} else {
    Set-UserPathEntries (@($entries) + $BinDir)
    Write-Ok "Added $BinDir to your PATH"
}

Write-Host ''
Write-Host '  Done. Next steps:' -ForegroundColor White
Write-Host '    1. Close this PowerShell window and open a new one.'
Write-Host '    2. Check the installation:   paygate --version'
Write-Host '    3. Add your merchant:         paygate init'
Write-Host ''
